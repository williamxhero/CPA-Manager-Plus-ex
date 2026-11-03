package plugin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const quotaFixture = `{"source":"bsk","plan":"Token Plan 个人版 Standard","planStatus":"生效中","observedAt":"2026-10-08T00:00:00+08:00","windows":[{"window":"1month","usedPercent":100.0,"resetTime":"2026-10-18T00:00:00+08:00"}],"metrics":[{"key":"balance_available","label":"余额","value":0,"unit":"CNY","currency":"CNY","format":"currency"}],"notes":[]}`

// Component helper protocol, derived from the pinned official three-window
// counts; unlike quotaFixture this is not the obsolete Token Plan contract.
const codingFixture = `{"protocol":"qwen-coding-plan-v1","product":"coding-plan","queryStatus":"success","authorityStatus":"needs_configuration","reason":"account_authority_unverified","observedAt":"2026-11-02T12:00:00Z","instanceType":"pro","windows":[{"window":"5h","unit":"requests","used":"38","total":"100","remaining":"62","resetTime":"2026-08-06T07:06:40Z"},{"window":"week","unit":"requests","used":"500","total":"1000","remaining":"500","resetTime":"2026-08-07T10:53:20Z"},{"window":"billing_month","unit":"requests","used":"950","total":"1000","remaining":"50","resetTime":"2026-08-08T14:40:00Z"}]}`

// This subprocess is the command stub; no shell or platform-specific executable
// is needed. It is entered only when the parent's argument slice requests it.
func TestQuotaCommandHelper(t *testing.T) {
	mode := ""
	for i, arg := range os.Args {
		if arg == "--quota-stub" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
		}
	}
	if mode == "" {
		return
	}
	switch mode {
	case "coding":
		fmt.Print(codingFixture)
	case "coding-nonzero":
		fmt.Print(codingFixture)
		os.Exit(3)
	case "forged":
		fmt.Print(strings.Replace(codingFixture, `"authorityStatus":"needs_configuration"`, `"authorityStatus":"verified"`, 1))
	case "hand-account":
		fmt.Print(strings.Replace(codingFixture, `"product":"coding-plan"`, `"product":"coding-plan","account":"user-entered-account","verified":true`, 1))
	case "login":
		fmt.Print(`{"success":true,"data":{"accountId":"login-account","verified":true}}`)
	case "invalid-unit":
		fmt.Print(strings.Replace(codingFixture, `"unit":"requests"`, `"unit":"tokens"`, 1))
	case "null-count":
		fmt.Print(strings.Replace(codingFixture, `"used":"38"`, `"used":null`, 1))
	case "wrong-remaining":
		fmt.Print(strings.Replace(codingFixture, `"remaining":"62"`, `"remaining":"100"`, 1))
	case "success":
		fmt.Print(quotaFixture)
	case "period":
		fmt.Print(strings.Replace(quotaFixture, `"planStatus":"生效中"`, `"planStatus":"生效中","planStart":"2026-09-17T18:11:24+08:00","planEnd":"2027-09-18T00:00:00+08:00","daysLeft":999`, 1))
	case "metrics-only":
		fmt.Print(`{"metrics":[{"key":"balance","label":"余额","value":0,"format":"number"}]}`)
	case "failure":
		fmt.Print(`{"error":"ConsoleNeedLogin","message":"CLI says login required"}`)
		os.Exit(3)
	case "zero-error":
		fmt.Print(`{"error":"ConsoleNeedLogin","message":"CLI says login required"}`)
	case "secret":
		fmt.Print(`{"error":"login","message":"dummy-config-key dummy-selected-key Cookie: session=private-cookie; token=private-token sk-sp-private-key"}`)
		os.Exit(2)
	case "garbage":
		fmt.Print("CLI garbage diagnostic")
		os.Exit(0)
	case "encoded-message":
		fmt.Print(`{"message":"ConsoleNeedLogin: FixtureAlpha%2Bopaque%2Fvalue"}`)
	case "encoded-garbage":
		fmt.Print("ConsoleNeedLogin: FixtureAlpha%252bopaque%252Fvalue")
	case "invalid-with-stderr":
		fmt.Print(`{}`)
		fmt.Fprint(os.Stderr, "ConsoleNeedLogin: helper requires login")
	case "coding-with-stderr":
		fmt.Print(codingFixture)
		fmt.Fprint(os.Stderr, "ordinary helper warning")
	case "empty":
		fmt.Print(`{"source":"bsk","windows":[],"metrics":[]}`)
	case "timeout":
		time.Sleep(10 * time.Second)
	case "stdout-overflow":
		fmt.Print(strings.Repeat("x", maxQuotaStdout+100))
	case "stderr-overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", maxQuotaStderr+100))
	case "stderr":
		fmt.Fprint(os.Stderr, "CLI stderr diagnostic")
		os.Exit(2)
	case "args":
		if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "space ; & argument" {
			os.Exit(4)
		}
		fmt.Print(quotaFixture)
	}
	os.Exit(0)
}
func commandManager(t *testing.T, mode string) *Manager {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(nil)
	m.cfg = providerConfig(t)
	m.cfg.Command = executable
	m.cfg.CommandArgs = []string{"-test.run=^TestQuotaCommandHelper$", "--", "--quota-stub", mode, "--protocol", "qwen-coding-plan-v1", "--source", "coding-plan", "--bl-path", "synthetic-bl", "--cache-file", "synthetic-cache"}
	m.cfg.CommandTimeout = 3 * time.Second
	return m
}
func quotaRequest() pluginapi.QuotaFetchRequest {
	return pluginapi.QuotaFetchRequest{Provider: ProviderID, Attributes: map[string]string{"api_key": "dummy-selected-key"}}
}

func TestQuotaDecodeFailuresKeepSafeCLIDiagnostics(t *testing.T) {
	for _, mode := range []string{"encoded-message", "encoded-garbage", "invalid-with-stderr"} {
		t.Run(mode, func(t *testing.T) {
			m := commandManager(t, mode)
			m.cfg.APIKeys = nil
			req := quotaRequest()
			req.Attributes["api_key"] = "FixtureAlpha+opaque/value"
			_, err := m.FetchQuota(context.Background(), req)
			if err == nil || strings.Contains(err.Error(), "FixtureAlpha") || !strings.Contains(err.Error(), "ConsoleNeedLogin") {
				t.Fatalf("unsafe or lost CLI diagnostic: %v", err)
			}
		})
	}
}

func TestQuotaValidContractAllowsStderrWarning(t *testing.T) {
	m := commandManager(t, "coding-with-stderr")
	result, err := m.FetchQuota(context.Background(), quotaRequest())
	if err != nil || len(result.Groups) != 1 || len(result.Groups[0].Buckets) != 3 {
		t.Fatalf("valid stdout rejected because of stderr: %#v %v", result, err)
	}
}

func TestQuotaCommandLegacyContractRejectedAndArgumentSlice(t *testing.T) {
	for _, mode := range []string{"success", "args"} {
		t.Run(mode, func(t *testing.T) {
			m := commandManager(t, mode)
			if mode == "args" {
				m.cfg.CommandArgs = append(m.cfg.CommandArgs, "space ; & argument")
			}
			result, err := m.FetchQuota(context.Background(), quotaRequest())
			if err == nil || !strings.Contains(err.Error(), "coding_plan_helper_protocol_required") || result.Subscription != nil || len(result.Groups) != 0 || len(result.Summary) != 0 {
				t.Fatalf("obsolete monthly/money contract became credential quota: %#v %v", result, err)
			}
		})
	}
}
func TestQuotaCommandFailuresReturnOwnCLIText(t *testing.T) {
	tests := map[string]string{"failure": "ConsoleNeedLogin: CLI says login required", "zero-error": "ConsoleNeedLogin: CLI says login required", "garbage": "CLI garbage diagnostic", "stderr": "CLI stderr diagnostic", "empty": "coding_plan_helper_protocol_required"}
	for mode, want := range tests {
		t.Run(mode, func(t *testing.T) {
			m := commandManager(t, mode)
			_, err := m.FetchQuota(context.Background(), quotaRequest())
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("got %v, want %s", err, want)
			}
		})
	}
}
func TestQuotaCommandHardTimeoutAndBoundedOutput(t *testing.T) {
	for _, mode := range []string{"timeout", "stdout-overflow", "stderr-overflow"} {
		t.Run(mode, func(t *testing.T) {
			m := commandManager(t, mode)
			m.cfg.CommandTimeout = 100 * time.Millisecond
			start := time.Now()
			_, err := m.FetchQuota(context.Background(), quotaRequest())
			if err == nil {
				t.Fatal("expected command failure")
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Fatalf("hard bound not honored: %v", elapsed)
			}
			if mode == "timeout" && !strings.Contains(err.Error(), "timed out") {
				t.Fatal(err)
			}
			if mode != "timeout" && !strings.Contains(err.Error(), "byte limit") {
				t.Fatal(err)
			}
		})
	}
}
func TestQuotaCommandSecretRedaction(t *testing.T) {
	m := commandManager(t, "secret")
	_, err := m.FetchQuota(context.Background(), quotaRequest())
	if err == nil {
		t.Fatal("expected CLI failure")
	}
	for _, secret := range []string{"dummy-config-key", "dummy-selected-key", "private-cookie", "private-token", "sk-sp-private-key"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("secret leaked: %v", err)
		}
	}
	if !strings.Contains(err.Error(), "login") {
		t.Fatal("lost CLI's own error")
	}
}
func TestQuotaMappingRejectsOutOfRangeAndAllowsMetricsOnly(t *testing.T) {
	for _, sample := range []struct {
		percent   float64
		remaining float64
	}{{0, 1}, {25, 0.75}, {100, 0}} {
		result, err := normalizeQuota([]byte(fmt.Sprintf(`{"windows":[{"window":"5h","usedPercent":%g}]}`, sample.percent)), "Qwen")
		if err != nil || result.Groups[0].Buckets[0].RemainingFraction != sample.remaining {
			t.Fatalf("valid mapping failed: %#v %v", result, err)
		}
	}
	for _, percent := range []float64{-20, -0.000001, 100.000001, 150} {
		t.Run(fmt.Sprintf("reject_%g", percent), func(t *testing.T) {
			result, err := normalizeQuota([]byte(fmt.Sprintf(`{"windows":[{"window":"5h","usedPercent":%g}]}`, percent)), "Qwen")
			if err == nil || len(result.Groups) != 0 || len(result.Summary) != 0 {
				t.Fatalf("out-of-range percentage silently clamped: %#v %v", result, err)
			}
		})
	}
	result, err := normalizeQuota([]byte(`{"metrics":[{"key":"balance","label":"Balance","value":0,"format":"number"}]}`), "Qwen")
	if err != nil || len(result.Groups) != 0 || len(result.Summary) != 1 {
		t.Fatalf("metrics-only reading failed: %#v %v", result, err)
	}
}
func TestQuotaNoReadingHonestyAndMissingValues(t *testing.T) {
	tests := map[string]string{
		"empty": `{}`, "null": `null`, "garbage": `garbage`, "error": `{"error":"login"}`,
		"missing_percent": `{"windows":[{"window":"1month"}]}`, "null_percent": `{"windows":[{"window":"1month","usedPercent":null}]}`, "missing_window": `{"windows":[{"usedPercent":0}]}`, "percent_overflow": `{"windows":[{"window":"1month","usedPercent":1e999}]}`,
		"invalid_reset":        `{"windows":[{"window":"1month","usedPercent":0,"resetTime":"not a date"}]}`,
		"missing_metric_value": `{"metrics":[{"key":"balance","label":"Balance"}]}`, "null_metric_value": `{"metrics":[{"key":"balance","label":"Balance","value":null}]}`, "missing_metric_key": `{"metrics":[{"label":"Balance","value":0}]}`, "invalid_metric_format": `{"metrics":[{"key":"balance","label":"Balance","value":0,"format":"pie"}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeQuota([]byte(body), "Qwen")
			if err == nil {
				t.Fatal("invented reading from invalid contract")
			}
		})
	}
	result, err := normalizeQuota([]byte(`{"windows":[{"window":"1month","usedPercent":100}]}`), "Qwen")
	if err != nil || result.Groups[0].Buckets[0].ResetTime != "" {
		t.Fatal("missing reset should stay missing, not be invented")
	}
}
func TestQuotaSelectedCredentialAndConfigurationValidation(t *testing.T) {
	m := commandManager(t, "success")
	req := quotaRequest()
	req.Provider = "other"
	if _, err := m.FetchQuota(context.Background(), req); err == nil {
		t.Fatal("accepted foreign credential")
	}
	req = quotaRequest()
	req.Attributes = nil
	if _, err := m.FetchQuota(context.Background(), req); err == nil {
		t.Fatal("fell back to config key")
	}
	m.cfg.Command = ""
	if _, err := m.FetchQuota(context.Background(), quotaRequest()); err == nil {
		t.Fatal("accepted unconfigured command")
	}
	m = commandManager(t, "success")
	m.cfg.Command = "missing-quota-command-executable"
	if _, err := m.FetchQuota(context.Background(), quotaRequest()); err == nil {
		t.Fatal("nonexistent executable succeeded")
	}
}
func TestSecretRedactionArgumentSecretsAndBoundedDiagnostics(t *testing.T) {
	cfg := providerConfig(t)
	cfg.CommandArgs = []string{"--cookie", "opaque-cookie", "--token=opaque-token"}
	got := redactSecrets("opaque-cookie opaque-token Bearer unrelated-token api_key=field-secret", cfg, "")
	for _, s := range []string{"opaque-cookie", "opaque-token", "unrelated-token", "field-secret"} {
		if strings.Contains(got, s) {
			t.Fatalf("leaked %s: %s", s, got)
		}
	}
	if len(boundedDiagnostic([]byte(strings.Repeat("x", maxQuotaError+100)))) > maxQuotaError+20 {
		t.Fatal("diagnostics unbounded")
	}
}
