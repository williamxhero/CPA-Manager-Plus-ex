package worker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/codexreset"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpaauthfiles"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const (
	codexUsageURL               = "https://chatgpt.com/backend-api/wham/usage"
	codexResetCreditsURL        = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	codexResetCreditsConsumeURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
)

type CodexCredential struct {
	CredentialKey string
	AuthIndex     string
	AccountID     string
}

type CodexResetCreditSnapshot struct {
	AvailableCount *int64
	DetailJSON     string
	ObservedAtMS   int64
}

type CodexResetCreditWorker struct {
	store     *store.Store
	client    *http.Client
	authFiles *cpaauthfiles.Client
	mu        sync.RWMutex
	cfg       collectorpkg.RuntimeConfig
	enabled   bool
}

// SetHTTPClient allows behavior tests to inject an httptest transport.
func (w *CodexResetCreditWorker) SetHTTPClient(client *http.Client) {
	if w == nil || client == nil {
		return
	}
	w.mu.Lock()
	w.client = client
	w.mu.Unlock()
}

func NewCodexResetCreditWorker(st *store.Store) *CodexResetCreditWorker {
	return &CodexResetCreditWorker{store: st, client: &http.Client{Timeout: 30 * time.Second}, authFiles: cpaauthfiles.New(nil), enabled: true}
}

func (w *CodexResetCreditWorker) SetEnabled(enabled bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.enabled = enabled
	w.mu.Unlock()
}

func (w *CodexResetCreditWorker) UpdateRuntimeConfig(_ context.Context, cfg collectorpkg.RuntimeConfig) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.cfg = cfg
	w.mu.Unlock()
}

// Start refreshes configured Codex credentials hourly. It intentionally does
// not consume credits: redemption is an explicit, ledger-guarded operation.
func (w *CodexResetCreditWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	go func() {
		_ = w.Refresh(ctx)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.Refresh(ctx)
			}
		}
	}()
}

func (w *CodexResetCreditWorker) Refresh(ctx context.Context) error {
	if w == nil || w.store == nil {
		return fmt.Errorf("codex reset worker is not configured")
	}
	w.mu.RLock()
	cfg, enabled := w.cfg, w.enabled
	w.mu.RUnlock()
	if !enabled || strings.TrimSpace(cfg.CPAUpstreamURL) == "" || strings.TrimSpace(cfg.ManagementKey) == "" {
		return nil
	}
	files, err := w.authFiles.Fetch(ctx, cfg.CPAUpstreamURL, cfg.ManagementKey)
	if err != nil {
		return err
	}
	for _, file := range files {
		if !strings.EqualFold(file.Provider, "codex") || file.Disabled {
			continue
		}
		if _, err := w.Fetch(ctx, cfg, CodexCredential{CredentialKey: file.Name, AuthIndex: file.AuthIndex, AccountID: file.AccountID}); err != nil {
			return err
		}
	}
	return nil
}

// Fetch obtains usage and reset-credit detail through CPA's token-substituting
// api-call endpoint. Raw provider credentials never leave CPA or enter logs.
func (w *CodexResetCreditWorker) Fetch(ctx context.Context, cfg collectorpkg.RuntimeConfig, credential CodexCredential) (CodexResetCreditSnapshot, error) {
	if w == nil || w.store == nil {
		return CodexResetCreditSnapshot{}, fmt.Errorf("codex reset worker is not configured")
	}
	if _, err := w.apiCall(ctx, cfg, credential, http.MethodGet, codexUsageURL, ""); err != nil {
		return CodexResetCreditSnapshot{}, err
	}
	detailBody, err := w.apiCall(ctx, cfg, credential, http.MethodGet, codexResetCreditsURL, "")
	if err != nil {
		return CodexResetCreditSnapshot{}, err
	}
	count, detail, cycle, ok := parseResetCreditPayload(detailBody)
	if !ok || count == nil {
		return CodexResetCreditSnapshot{}, fmt.Errorf("Codex reset-credit count is unavailable")
	}
	observed := time.Now().UnixMilli()
	if err := w.store.CodexResetCredits.RecordObservation(ctx, codexreset.LedgerEntry{CredentialKey: credential.CredentialKey, CycleKey: cycle, Status: "observed", AvailableCount: count, DetailJSON: detail}); err != nil {
		return CodexResetCreditSnapshot{}, err
	}
	return CodexResetCreditSnapshot{AvailableCount: count, DetailJSON: detail, ObservedAtMS: observed}, nil
}

// Consume performs a fresh preflight, claims a unique request in the durable
// ledger, redeems once, and performs a fresh postflight read.
func (w *CodexResetCreditWorker) Consume(ctx context.Context, cfg collectorpkg.RuntimeConfig, credential CodexCredential) (CodexResetCreditSnapshot, error) {
	before, err := w.Fetch(ctx, cfg, credential)
	if err != nil {
		return CodexResetCreditSnapshot{}, err
	}
	cycle := resetCycleKey(before.DetailJSON)
	requestID, err := newRedeemRequestID()
	if err != nil {
		return CodexResetCreditSnapshot{}, err
	}
	claimed, err := w.store.CodexResetCredits.ClaimConsumption(ctx, credential.CredentialKey, cycle, requestID)
	if err != nil {
		return CodexResetCreditSnapshot{}, err
	}
	if !claimed {
		return CodexResetCreditSnapshot{}, fmt.Errorf("reset credit already claimed for credential cycle")
	}
	body, _ := json.Marshal(map[string]string{"redeem_request_id": requestID})
	if _, err := w.apiCall(ctx, cfg, credential, http.MethodPost, codexResetCreditsConsumeURL, string(body)); err != nil {
		_ = w.store.CodexResetCredits.MarkFailed(ctx, credential.CredentialKey, cycle, requestID, err.Error())
		return CodexResetCreditSnapshot{}, err
	}
	if err := w.store.CodexResetCredits.MarkConsumed(ctx, credential.CredentialKey, cycle, requestID, time.Now().UnixMilli()); err != nil {
		return CodexResetCreditSnapshot{}, err
	}
	return w.Fetch(ctx, cfg, credential)
}

func (w *CodexResetCreditWorker) apiCall(ctx context.Context, cfg collectorpkg.RuntimeConfig, credential CodexCredential, method, target, data string) ([]byte, error) {
	payload, err := json.Marshal(map[string]any{"authIndex": credential.AuthIndex, "method": method, "url": target, "header": map[string]string{"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json", "Chatgpt-Account-Id": credential.AccountID}, "data": data})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cpa.NormalizeBaseURL(cfg.CPAUpstreamURL)+"/v0/management/api-call", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.ManagementKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	dataBytes, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("CPA api-call status %d", res.StatusCode)
	}
	var envelope struct {
		StatusCode int             `json:"status_code"`
		Body       json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(dataBytes, &envelope); err != nil {
		return nil, err
	}
	if envelope.StatusCode < 200 || envelope.StatusCode >= 300 {
		return nil, fmt.Errorf("Codex upstream status %d", envelope.StatusCode)
	}
	return envelope.Body, nil
}

func parseResetCreditPayload(body []byte) (*int64, string, string, bool) {
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return nil, string(body), "", false
	}
	var value any = raw["rate_limit_reset_credits"]
	if value == nil {
		value = raw["rateLimitResetCredits"]
	}
	credits, ok := value.(map[string]any)
	if !ok {
		return nil, string(body), "", false
	}
	var count *int64
	if number, ok := credits["available_count"].(float64); ok {
		v := int64(number)
		count = &v
	}
	cycle := cycleValue(raw)
	if cycle == "" {
		cycle = cycleValue(credits)
	}
	return count, string(body), cycle, cycle != ""
}

func resetCycleKey(detail string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(detail), &payload) == nil {
		if cycle := cycleValue(payload); cycle != "" {
			return cycle
		}
		if value, ok := payload["rate_limit_reset_credits"].(map[string]any); ok {
			return cycleValue(value)
		}
		if value, ok := payload["rateLimitResetCredits"].(map[string]any); ok {
			return cycleValue(value)
		}
	}
	return ""
}

func cycleValue(payload map[string]any) string {
	for _, key := range []string{"reset_at", "resetAt", "cycle_id", "cycleId", "id"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func newRedeemRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
