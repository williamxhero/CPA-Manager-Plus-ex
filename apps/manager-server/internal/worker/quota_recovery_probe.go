package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// Supported CPA management endpoints used by the recovery probe. Both are
// registered by the CPA core (server_management.go); CPAMP already calls
// api-call for the same purpose in internal/service/codexinspection.
const (
	quotaFetchManagementPath = "/v0/management/quota/fetch"
	apiCallManagementPath    = "/v0/management/api-call"

	// Provider usage endpoints mirrored from internal/service/codexinspection
	// (codexUsageURL / xaiBillingMonthlyURL). They are only reachable through
	// the api-call management endpoint, which substitutes $TOKEN$ for the
	// credential token server-side; CPAMP never reads the raw key.
	codexUsageProbeURL = "https://chatgpt.com/backend-api/wham/usage"
	xaiUsageProbeURL   = "https://cli-chat-proxy.grok.com/v1/billing"

	quotaProbeResponseLimit = 1 << 20
)

// quotaRecoveryProbe reports the current quota windows for a credential so the
// recovery gate can confirm the provider quota actually recovered before it
// re-enables a credential CPAMP disabled. It is injectable so tests can supply
// deterministic fake windows instead of reaching a real provider.
type quotaRecoveryProbe interface {
	ProbeQuotaRecovery(ctx context.Context, baseURL, managementKey, provider, authIndex, accountID string) (quotaProbeResult, error)
}

// quotaProbeResult is the outcome of a recovery probe. Available=false means no
// authoritative quota windows could be read; the caller must then behave
// conservatively and must never treat the absence of data as recovery.
type quotaProbeResult struct {
	Available bool
	Windows   []quotaProbeWindow
}

// quotaProbeWindow is one quota window reported for a credential. Exhausted is
// true when the window is at or over its limit.
type quotaProbeWindow struct {
	WindowKind string
	Exhausted  bool
}

// AnyExhausted reports whether any applicable window is still at its limit.
func (r quotaProbeResult) AnyExhausted() bool {
	for _, w := range r.Windows {
		if w.Exhausted {
			return true
		}
	}
	return false
}

type httpQuotaRecoveryProbe struct {
	client *http.Client
}

func newHTTPQuotaRecoveryProbe() *httpQuotaRecoveryProbe {
	return &httpQuotaRecoveryProbe{client: &http.Client{Timeout: quotaAutoDisableActionTimeout}}
}

// ProbeQuotaRecovery prefers the normalized quota/fetch endpoint and falls back
// to a provider usage request through api-call. Both deliver real HTTP status
// codes; a transport/protocol error is returned so the caller can back off.
func (p *httpQuotaRecoveryProbe) ProbeQuotaRecovery(ctx context.Context, baseURL, managementKey, provider, authIndex, accountID string) (quotaProbeResult, error) {
	baseURL = strings.TrimSpace(baseURL)
	managementKey = strings.TrimSpace(managementKey)
	authIndex = strings.TrimSpace(authIndex)
	if baseURL == "" || managementKey == "" || authIndex == "" {
		return quotaProbeResult{}, nil
	}
	res, err := p.fetchNormalizedQuota(ctx, baseURL, managementKey, provider, authIndex)
	if err != nil {
		return quotaProbeResult{}, err
	}
	if res.Available {
		return res, nil
	}
	targetURL := providerUsageProbeURL(provider)
	if targetURL == "" {
		return quotaProbeResult{}, nil
	}
	return p.fetchProviderUsage(ctx, baseURL, managementKey, authIndex, accountID, targetURL)
}

func (p *httpQuotaRecoveryProbe) fetchNormalizedQuota(ctx context.Context, baseURL, managementKey, provider, authIndex string) (quotaProbeResult, error) {
	payload, err := json.Marshal(map[string]any{
		"authIndex": authIndex,
		"provider":  provider,
	})
	if err != nil {
		return quotaProbeResult{}, err
	}
	body, status, err := p.postManagement(ctx, baseURL, managementKey, quotaFetchManagementPath, payload)
	if err != nil {
		return quotaProbeResult{}, err
	}
	if status < 200 || status >= 300 {
		// 501 (no quota provider) and 404 mean no normalized source is available;
		// fall through to the provider usage endpoint instead of failing hard.
		return quotaProbeResult{}, nil
	}
	windows := windowsFromQuotaFetchResponse(body)
	if len(windows) == 0 {
		return quotaProbeResult{}, nil
	}
	return quotaProbeResult{Available: true, Windows: windows}, nil
}

func (p *httpQuotaRecoveryProbe) fetchProviderUsage(ctx context.Context, baseURL, managementKey, authIndex, accountID, targetURL string) (quotaProbeResult, error) {
	header := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
	}
	if strings.TrimSpace(accountID) != "" {
		header["Chatgpt-Account-Id"] = strings.TrimSpace(accountID)
	}
	payload, err := json.Marshal(map[string]any{
		"authIndex": authIndex,
		"method":    http.MethodGet,
		"url":       targetURL,
		"header":    header,
	})
	if err != nil {
		return quotaProbeResult{}, err
	}
	body, status, err := p.postManagement(ctx, baseURL, managementKey, apiCallManagementPath, payload)
	if err != nil {
		return quotaProbeResult{}, err
	}
	if status < 200 || status >= 300 {
		// The management envelope itself failed; treat as no data available.
		return quotaProbeResult{}, nil
	}
	innerStatus, innerBody := decodeAPICallEnvelope(body)
	if innerStatus != 0 && (innerStatus < 200 || innerStatus >= 300) {
		return quotaProbeResult{}, nil
	}
	windows := windowsFromProviderUsageBody(innerBody)
	if len(windows) == 0 {
		return quotaProbeResult{}, nil
	}
	return quotaProbeResult{Available: true, Windows: windows}, nil
}

func (p *httpQuotaRecoveryProbe) postManagement(ctx context.Context, baseURL, managementKey, path string, payload []byte) ([]byte, int, error) {
	client := p.client
	if client == nil {
		client = &http.Client{Timeout: quotaAutoDisableActionTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cpa.NormalizeBaseURL(baseURL)+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+managementKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, quotaProbeResponseLimit))
	if err != nil {
		return nil, res.StatusCode, err
	}
	return body, res.StatusCode, nil
}

// windowsFromQuotaFetchResponse parses the normalized quota/fetch envelope. A
// bucket without any numeric usage evidence is ignored rather than assumed
// recovered.
func windowsFromQuotaFetchResponse(body []byte) []quotaProbeWindow {
	var raw struct {
		Groups []struct {
			Buckets []map[string]any `json:"buckets"`
		} `json:"groups"`
		Buckets []map[string]any `json:"buckets"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	windows := make([]quotaProbeWindow, 0, len(raw.Buckets))
	for _, bucket := range raw.Buckets {
		if window, ok := quotaWindowFromObject(bucket, "window"); ok {
			windows = append(windows, window)
		}
	}
	for _, group := range raw.Groups {
		for _, bucket := range group.Buckets {
			if window, ok := quotaWindowFromObject(bucket, "window"); ok {
				windows = append(windows, window)
			}
		}
	}
	return windows
}

// windowsFromProviderUsageBody walks a provider usage payload and picks out any
// object that carries both a window identifier and numeric usage evidence.
func windowsFromProviderUsageBody(body []byte) []quotaProbeWindow {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return nil
	}
	windows := make([]quotaProbeWindow, 0, 4)
	collectProviderUsageWindows(decoded, "window", &windows)
	return windows
}

func collectProviderUsageWindows(value any, key string, out *[]quotaProbeWindow) {
	switch typed := value.(type) {
	case map[string]any:
		if window, ok := quotaWindowFromObject(typed, key); ok {
			*out = append(*out, window)
		}
		for childKey, child := range typed {
			collectProviderUsageWindows(child, childKey, out)
		}
	case []any:
		for index, child := range typed {
			collectProviderUsageWindows(child, key+"["+strconv.Itoa(index)+"]", out)
		}
	}
}

func quotaWindowFromObject(obj map[string]any, fallbackKey string) (quotaProbeWindow, bool) {
	used, hasUsed := numericField(obj, "used_percent", "usedPercent")
	remainingFraction, hasFraction := numericField(obj, "remainingFraction", "remaining_fraction")
	remainingPercent, hasPercent := numericField(obj, "remaining_percent", "remainingPercent")
	if !hasUsed && !hasFraction && !hasPercent {
		return quotaProbeWindow{}, false
	}
	exhausted := false
	switch {
	case hasUsed && used >= 100:
		exhausted = true
	case hasFraction && remainingFraction <= 0:
		exhausted = true
	case hasPercent && remainingPercent <= 0:
		exhausted = true
	}
	return quotaProbeWindow{
		WindowKind: quotaProbeWindowKind(obj, fallbackKey),
		Exhausted:  exhausted,
	}, true
}

func quotaProbeWindowKind(obj map[string]any, fallbackKey string) string {
	for _, key := range []string{"window_kind", "windowKind", "window", "provider_window_id", "providerWindowId"} {
		if value, ok := obj[key].(string); ok && strings.TrimSpace(value) != "" {
			return normalizeQuotaProbeWindowKind(value)
		}
	}
	if seconds, ok := numericField(obj, "limit_window_seconds", "limitWindowSeconds"); ok {
		return normalizeQuotaProbeWindowKind(strconv.FormatInt(int64(seconds), 10) + "s")
	}
	return normalizeQuotaProbeWindowKind(fallbackKey)
}

// normalizeQuotaProbeWindowKind collapses the many provider spellings into the
// SPEC's five_hour / weekly / monthly buckets where possible.
func normalizeQuotaProbeWindowKind(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.TrimSuffix(normalized, "_window")
	if seconds, err := strconv.ParseInt(strings.TrimSuffix(normalized, "s"), 10, 64); err == nil {
		switch {
		case seconds >= 17_000 && seconds <= 19_000: // ~5h
			return "five_hour"
		case seconds >= 600_000 && seconds <= 620_000: // ~7d
			return "weekly"
		case seconds >= 2_400_000 && seconds <= 2_700_000: // ~30d
			return "monthly"
		default:
			return "unknown"
		}
	}
	switch normalized {
	case "five_hour", "five-hour", "5h", "5-hour":
		return "five_hour"
	case "weekly", "week", "7d", "7-day":
		return "weekly"
	case "monthly", "month", "30d", "30-day":
		return "monthly"
	case "rolling_24h", "rolling-24h", "24h":
		return "rolling_24h"
	default:
		if normalized == "" {
			return "unknown"
		}
		return normalized
	}
}

func numericField(obj map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, ok := obj[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed, true
		case int:
			return float64(typed), true
		case int64:
			return float64(typed), true
		case json.Number:
			if parsed, err := typed.Float64(); err == nil {
				return parsed, true
			}
		case string:
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

// decodeAPICallEnvelope extracts the nested status_code and body from the CPA
// api-call response. A missing nested status means the upstream status was not
// reported, in which case only the body is used.
func decodeAPICallEnvelope(body []byte) (int, []byte) {
	var raw struct {
		StatusCode json.RawMessage `json:"status_code"`
		StatusCode2 json.RawMessage `json:"statusCode"`
		Body       json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, nil
	}
	status := 0
	statusRaw := raw.StatusCode
	if len(statusRaw) == 0 {
		statusRaw = raw.StatusCode2
	}
	if len(statusRaw) > 0 {
		var num float64
		if err := json.Unmarshal(statusRaw, &num); err == nil {
			status = int(num)
		} else {
			var str string
			if err := json.Unmarshal(statusRaw, &str); err == nil {
				if parsed, err := strconv.Atoi(strings.TrimSpace(str)); err == nil {
					status = parsed
				}
			}
		}
	}
	inner := []byte(nil)
	if len(raw.Body) > 0 {
		var asString string
		if err := json.Unmarshal(raw.Body, &asString); err == nil {
			inner = []byte(asString)
		} else {
			inner = raw.Body
		}
	}
	return status, inner
}

func providerUsageProbeURL(provider string) string {
	switch normalizeQuotaProvider(provider) {
	case "codex":
		return codexUsageProbeURL
	case "xai":
		return xaiUsageProbeURL
	default:
		return ""
	}
}
