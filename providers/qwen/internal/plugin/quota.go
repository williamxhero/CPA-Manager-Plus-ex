package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"

	"cpa-credential-qwen/internal/bailianquota"
	"cpa-credential-qwen/internal/config"
	"cpa-credential-qwen/internal/quotawire"
)

const maxQuotaStdout = quotawire.MaxStdout
const maxQuotaStderr = quotawire.MaxStderr
const maxQuotaError = quotawire.MaxDiagnostic

// FetchQuota queries Coding Plan only. CLI/cache results are candidate readings
// for Core enrollment/receipt validation, not proof of selected-key ownership.
// Raw-token queries remain unbound and return no policy quota.
func (m *Manager) FetchQuota(ctx context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
	empty := pluginapi.QuotaFetchResponse{}
	if req.Provider != ProviderID {
		return empty, fmt.Errorf("unsupported quota provider")
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	key, baseURL, err := resolveCredential(req.Attributes, req.StorageJSON, cfg)
	if err != nil {
		return empty, err
	}
	// Indexed requests must refer to the current persisted key and endpoint.
	// This locator fence does not establish Coding Plan account ownership.
	// Unindexed callers retain the existing native API compatibility behavior.
	if req.AuthIndex != "" {
		seed, err := credentialIndexSeed(key, baseURL, cfg)
		if err != nil || req.Attributes["auth_index_seed"] != seed || req.AuthIndex != credentialAuthIndex(seed) {
			return empty, fmt.Errorf("quota credential identity does not match current key and endpoint")
		}
	}
	if resp, ok := monthlyBLQuota(cfg); ok && codingPlanCLIArgs(cfg.CommandArgs) {
		return resp, nil
	}
	reading, err := fetchCommandQuota(ctx, cfg, key)
	if err != nil {
		return empty, err
	}
	if !codingPlanCLIArgs(cfg.CommandArgs) {
		return empty, fmt.Errorf("needs_configuration: account_authority_unverified")
	}
	return normalizeCodingPlanQuota(reading, cfg.DisplayName)
}

func codingPlanCLIArgs(args []string) bool {
	seenSource, seenProtocol, seenCLI, seenCache, seenCacheOnly, seenMaxAge, seenToken := false, false, false, false, false, false, false
	cacheOnlyValue := ""
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !hasValue && i+1 < len(args) && !strings.HasPrefix(strings.TrimSpace(args[i+1]), "--") {
			value = strings.TrimSpace(args[i+1])
			i++
		}
		switch name {
		case "source":
			seenSource = seenSource || value == "coding-plan"
		case "protocol":
			seenProtocol = seenProtocol || value == bailianquota.CodingPlanProtocol
		case "bl-path":
			seenCLI = seenCLI || value != ""
		case "cache-file":
			seenCache = seenCache || value != ""
		case "cache-only":
			seenCacheOnly = true
			cacheOnlyValue = value
		case "max-age":
			seenMaxAge = seenMaxAge || value != ""
		case "token-file", "console-site", "console-region":
			seenToken = true
		}
	}
	cacheOnlyRequested := seenCacheOnly && (cacheOnlyValue == "" || strings.EqualFold(cacheOnlyValue, "true"))
	return seenSource && seenProtocol && seenCache && (seenCLI || cacheOnlyRequested && seenMaxAge) && !seenToken
}

func normalizeCodingPlanQuota(raw bailianquota.CodingPlanResult, displayName string) (pluginapi.QuotaFetchResponse, error) {
	if raw.QueryStatus != "success" || raw.AuthorityStatus != "needs_configuration" || len(raw.Windows) != 3 {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("quota command returned an invalid Coding Plan result")
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = config.DefaultDisplayName
	}
	observed, err := time.Parse(time.RFC3339Nano, raw.ObservedAt)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("quota command returned an invalid Coding Plan observation time")
	}
	buckets := make([]pluginapi.QuotaBucket, 0, len(raw.Windows))
	for _, window := range raw.Windows {
		used, total, ok := decimalPair(window.Used, window.Total)
		if !ok || total.Sign() == 0 || used.Cmp(total) > 0 || strings.TrimSpace(window.ResetTime) == "" {
			return pluginapi.QuotaFetchResponse{}, fmt.Errorf("quota command returned an invalid Coding Plan window")
		}
		if _, err := time.Parse(time.RFC3339Nano, window.ResetTime); err != nil {
			return pluginapi.QuotaFetchResponse{}, fmt.Errorf("quota command returned an invalid Coding Plan reset time")
		}
		remainingBig := new(big.Rat).SetFrac(new(big.Int).Sub(new(big.Int).Set(total), used), total)
		remaining, _ := new(big.Float).SetPrec(256).SetRat(remainingBig).Float64()
		buckets = append(buckets, pluginapi.QuotaBucket{Window: window.Window, RemainingFraction: remaining, ResetTime: window.ResetTime})
	}
	return pluginapi.QuotaFetchResponse{Subscription: &pluginapi.QuotaSubscription{Plan: "coding-plan"}, Groups: []pluginapi.QuotaGroup{{DisplayName: displayName, Buckets: buckets}}, ObservedAtMS: observed.UnixMilli(), TimestampProvenance: "local_cli_acquisition_time", Protocol: bailianquota.CodingPlanProtocol, AccountProvenance: "configured_cli_profile"}, nil
}

func decimalPair(used, total string) (*big.Int, *big.Int, bool) {
	u, okU := new(big.Int).SetString(used, 10)
	t, okT := new(big.Int).SetString(total, 10)
	return u, t, okU && okT && used != "" && total != "" && !strings.HasPrefix(used, "-") && !strings.HasPrefix(total, "-")
}

// The native quota API and embedded page share command execution, validation and
// error handling, so the page cannot manufacture a reading rejected by the API.
func fetchCommandQuota(ctx context.Context, cfg config.Config, key string) (bailianquota.CodingPlanResult, error) {
	empty := bailianquota.CodingPlanResult{}
	if cfg.QuotaSource != "command" || cfg.Command == "" || cfg.CommandTimeout <= 0 {
		return empty, fmt.Errorf("quota command is not configured")
	}
	sources, protocols := 0, 0
	cliPath, directConfig, cacheOnly := false, false, false
	cacheFile := ""
	for i, arg := range cfg.CommandArgs {
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		if !hasValue && i+1 < len(cfg.CommandArgs) {
			value = cfg.CommandArgs[i+1]
		}
		switch name {
		case "source":
			sources++
			if value == "coding-plan" {
				continue
			}
		case "protocol":
			protocols++
			if value == bailianquota.CodingPlanProtocol {
				continue
			}
		case "bl-path":
			cliPath = true
			if strings.TrimSpace(value) != "" {
				continue
			}
		case "token-file", "console-region", "console-site":
			directConfig = true
			continue
		case "cache-file":
			cacheFile = value
			if strings.TrimSpace(value) != "" {
				continue
			}
		case "cache-only":
			if !hasValue {
				cacheOnly = true
				continue
			}
			parsed, err := strconv.ParseBool(value)
			if err == nil {
				cacheOnly = parsed
				continue
			}
		case "max-age":
			if age, err := time.ParseDuration(value); err == nil && age > 0 {
				continue
			}
		case "browser", "cookie", "cookie-file", "check", "token", "account", "verified":
		default:
			continue // The reviewed helper rejects unknown flags before I/O.
		}
		return empty, fmt.Errorf("needs_configuration: coding_plan_readonly_configuration_required")
	}
	if sources != 1 || protocols != 1 || cliPath && directConfig || cacheOnly && cacheFile == "" {
		return empty, fmt.Errorf("needs_configuration: coding_plan_readonly_configuration_required")
	}
	secrets := []string{key}
	for _, entry := range cfg.APIKeys {
		secrets = append(secrets, entry.Value)
	}
	out, errOut, runErr := quotawire.RunWithDiagnostics(ctx, cfg.Command, cfg.CommandArgs, "", cfg.CommandTimeout, secrets)
	safeError := func(message string) (bailianquota.CodingPlanResult, error) {
		return empty, fmt.Errorf("%s", quotawire.Sanitize(message, cfg.CommandArgs, secrets))
	}
	if runErr != nil {
		return safeError(runErr.Error())
	}
	raw, err := bailianquota.DecodeCodingPlanResult(out)
	if err != nil {
		return safeError(commandErrorText(out, errOut, err.Error()))
	}
	return raw, nil
}

// Preserve the CLI's own structured errors on both zero and nonzero exits.
// Non-JSON diagnostics are returned only through the redaction/bounding edge.
// monthlyBLQuota reads the one monthly window the cache writer actually stores.
// It does not add 5-hour or weekly windows.
func monthlyBLQuota(cfg config.Config) (pluginapi.QuotaFetchResponse, bool) {
	cacheFile, maxAge, ok := cacheOnlyReading(cfg.CommandArgs)
	if !ok {
		return pluginapi.QuotaFetchResponse{}, false
	}
	file, err := os.Open(cacheFile)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, false
	}
	body, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
	file.Close()
	if readErr != nil || len(body) == 0 || len(body) > 1<<20 {
		return pluginapi.QuotaFetchResponse{}, false
	}
	var saved struct {
		Source     string `json:"source"`
		ObservedAt string `json:"observedAt"`
		Windows    []struct {
			Window      string   `json:"window"`
			UsedPercent *float64 `json:"usedPercent"`
			ResetTime   string   `json:"resetTime"`
		} `json:"windows"`
	}
	if json.Unmarshal(body, &saved) != nil || saved.Source != "bl" || len(saved.Windows) != 1 {
		return pluginapi.QuotaFetchResponse{}, false
	}
	window := saved.Windows[0]
	if window.Window != "1month" || window.UsedPercent == nil || *window.UsedPercent < 0 || *window.UsedPercent > 100 {
		return pluginapi.QuotaFetchResponse{}, false
	}
	observed, err := time.Parse(time.RFC3339Nano, saved.ObservedAt)
	if err != nil || observed.After(time.Now()) || time.Since(observed) > maxAge {
		return pluginapi.QuotaFetchResponse{}, false
	}
	reset, err := time.Parse(time.RFC3339Nano, window.ResetTime)
	if err != nil || !reset.After(time.Now()) {
		return pluginapi.QuotaFetchResponse{}, false
	}
	remaining := (100 - *window.UsedPercent) / 100
	return pluginapi.QuotaFetchResponse{
		Groups:              []pluginapi.QuotaGroup{{Buckets: []pluginapi.QuotaBucket{{Window: "billing_month", RemainingFraction: remaining, ResetTime: window.ResetTime}}}},
		ObservedAtMS:        observed.UnixMilli(),
		TimestampProvenance: "local_cli_acquisition_time",
		Protocol:            bailianquota.CodingPlanProtocol,
		AccountProvenance:   "configured_cli_profile",
	}, true
}

func cacheOnlyReading(args []string) (string, time.Duration, bool) {
	var cacheFile, maxAge string
	cacheOnly := false
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !hasValue && i+1 < len(args) && !strings.HasPrefix(strings.TrimSpace(args[i+1]), "--") {
			value = strings.TrimSpace(args[i+1])
			i++
		}
		switch name {
		case "cache-file":
			cacheFile = value
		case "max-age":
			maxAge = value
		case "cache-only":
			cacheOnly = value == "" || strings.EqualFold(value, "true")
		}
	}
	age, err := time.ParseDuration(maxAge)
	if !cacheOnly || cacheFile == "" || err != nil || age <= 0 || age > 45*time.Minute {
		return "", 0, false
	}
	return cacheFile, age, true
}

func commandErrorText(stdout, stderr []byte, fallback string) string {
	return quotawire.Diagnostic(stdout, stderr, fallback)
}
func boundedDiagnostic(body []byte) string { return quotawire.BoundDiagnostic(body) }

type commandQuota = quotawire.Result

func normalizeQuota(body []byte, displayName string) (pluginapi.QuotaFetchResponse, error) {
	return quotawire.Decode(body, displayName)
}
func normalizeCommandQuota(raw commandQuota, displayName string) (pluginapi.QuotaFetchResponse, error) {
	return quotawire.Normalize(raw, displayName)
}
