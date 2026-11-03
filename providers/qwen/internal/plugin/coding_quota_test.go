package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qwen-cliproxyapi/internal/bailianquota"
)

type codingQuotaTransport func(*http.Request) (*http.Response, error)

func (f codingQuotaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Runs the real exported helper CLI in a synthetic subprocess. Only the upstream
// HTTP boundary is replaced; no browser, official CLI, account or network is used.
func TestCodingQuotaCLIProcess(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--coding-quota-cli" || i+1 >= len(os.Args) {
			continue
		}
		capture := os.Args[i+1]
		var operations []string
		http.DefaultTransport = codingQuotaTransport(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(body))
			var envelope struct {
				API string `json:"Api"`
			}
			_ = json.Unmarshal([]byte(form.Get("params")), &envelope)
			operations = append(operations, envelope.API)
			data := `{}`
			if strings.Contains(envelope.API, "tokenplan/personal/api/v2/usage") {
				data = `{"per1MonthPercentage":0.5,"per1MonthResetTime":1786000000000}`
			}
			if strings.Contains(envelope.API, "tokenplan/personal/api/v2/subscription") {
				data = `{"specCode":"standard","status":"VALID"}`
			}
			if strings.Contains(envelope.API, "queryCodingPlanInstanceInfoV2") {
				data = `{"codingPlanInstanceInfos":[{"status":"VALID","instanceType":"pro","codingPlanQuotaInfo":{"per5HourUsedQuota":38,"per5HourTotalQuota":100,"per5HourQuotaNextRefreshTime":1786000000000,"perWeekUsedQuota":500,"perWeekTotalQuota":1000,"perWeekQuotaNextRefreshTime":1786100000000,"perBillMonthUsedQuota":950,"perBillMonthTotalQuota":1000,"perBillMonthQuotaNextRefreshTime":1786200000000}}]}`
			}
			response := `{"code":"200","successResponse":true,"data":{"success":true,"DataV2":{"success":true,"data":{"success":true,"code":"SUCCESS","data":` + data + `}}}}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
		})
		exit := bailianquota.RunCLI(context.Background(), os.Args[i+2:], os.Stdout, os.Stderr)
		encoded, _ := json.Marshal(operations)
		if err := os.WriteFile(capture, encoded, 0600); err != nil {
			fmt.Fprintln(os.Stderr, "cannot save synthetic upstream receipt")
			os.Exit(2)
		}
		os.Exit(exit)
	}
}

func TestCodingQuotaLaneRejectsLegacyProductQuery(t *testing.T) {
	dir := t.TempDir()
	cookie := filepath.Join(dir, "synthetic-session.txt")
	if err := os.WriteFile(cookie, []byte("session=synthetic-only"), 0600); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(dir, "operations.json")
	m := commandManager(t, "success")
	m.cfg.CommandArgs = []string{"-test.run=^TestCodingQuotaCLIProcess$", "--", "--coding-quota-cli", capture, "--source", "cookie", "--cookie-file", cookie}
	result, err := m.FetchQuota(context.Background(), quotaRequest())
	var operations []string
	if body, readErr := os.ReadFile(capture); readErr == nil {
		if json.Unmarshal(body, &operations) != nil {
			t.Fatal("invalid controlled-upstream receipt")
		}
	} else if !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if err == nil || !strings.Contains(err.Error(), "needs_configuration") || len(result.Groups) != 0 || len(operations) != 0 {
		t.Fatalf("credential lane accepted/executed legacy product query: operations=%v groups=%v err=%v; want needs_configuration before legacy I/O", operations, result.Groups, err)
	}
}

func TestCodingQuotaQuerySuccessIsNotSelectedKeyAuthority(t *testing.T) {
	dir := t.TempDir()
	token := filepath.Join(dir, "synthetic-console-token.txt")
	if err := os.WriteFile(token, []byte("synthetic-console-token"), 0600); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(dir, "coding-operations.json")
	m := managementCommandManager(t, "success")
	m.cfg.CommandArgs = []string{"-test.run=^TestCodingQuotaCLIProcess$", "--", "--coding-quota-cli", capture,
		"--protocol", "qwen-coding-plan-v1", "--source", "coding-plan", "--token-file", token, "--console-region", "cn-beijing", "--console-site", "domestic"}
	resp, _ := managementUsage(t, m, `{"auth_index":"host-index-0"}`)
	var wire struct {
		Cards []struct {
			QueryStatus, AuthorityStatus string
			Error                        *string
			Windows, Metrics             []json.RawMessage
		}
	}
	if json.Unmarshal(resp.Body, &wire) != nil || resp.StatusCode != 200 || len(wire.Cards) != 1 {
		t.Fatalf("missing selected credential status: %s", resp.Body)
	}
	card := wire.Cards[0]
	if card.QueryStatus != "success" || card.AuthorityStatus != "needs_configuration" || card.Error == nil || !strings.Contains(*card.Error, "account_authority_unverified") || len(card.Windows) != 0 || len(card.Metrics) != 0 {
		t.Fatalf("direct-token query must remain unbound and separate from CLI policy data: %s", resp.Body)
	}
	operations, err := os.ReadFile(capture)
	if err != nil || string(operations) != `["zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2"]` {
		t.Fatalf("wrong product request: %s (%v)", operations, err)
	}
	result, err := m.FetchQuota(context.Background(), quotaRequest())
	if err == nil || !strings.Contains(err.Error(), "needs_configuration") || !strings.Contains(err.Error(), "account_authority_unverified") || len(result.Groups) != 0 || result.Subscription != nil || len(result.Summary) != 0 {
		t.Fatalf("unverified console result became recoverable quota: %#v %v", result, err)
	}
}

func TestCodingQuotaLaneRejectsForgedAuthorityAndLegacyContract(t *testing.T) {
	for _, mode := range []string{"coding", "forged", "hand-account", "login", "success", "period", "metrics-only"} {
		t.Run(mode, func(t *testing.T) {
			m := managementCommandManager(t, mode)
			// Host selection is not ownership evidence, even if a local caller
			// supplies ordinary login/account or "verified" metadata.
			req := quotaRequest()
			req.Attributes["account"] = "user-entered-account"
			req.Attributes["verified"] = "true"
			result, err := m.FetchQuota(context.Background(), req)
			if mode == "coding" {
				if err != nil || result.Subscription == nil || len(result.Groups) != 1 || len(result.Groups[0].Buckets) != 3 {
					t.Fatalf("configured CLI result was not normalized: %#v %v", result, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "needs_configuration") || result.Subscription != nil || len(result.Groups) != 0 || len(result.Summary) != 0 {
				t.Fatalf("unverified authority became policy quota: %#v %v", result, err)
			}
			resp, cards := managementUsage(t, m, `{}`)
			if resp.StatusCode != 200 || len(cards) == 0 || cards[0].Error == nil || len(cards[0].Windows) != 0 || len(cards[0].Metrics) != 0 || cards[0].AuthorityStatus != "needs_configuration" {
				t.Fatalf("unverified authority became key card quota: %s", resp.Body)
			}
			wantQueryStatus := "failed"
			if mode == "coding" {
				wantQueryStatus = "success"
			}
			if cards[0].QueryStatus != wantQueryStatus {
				t.Fatalf("query success and authority were conflated: %s", resp.Body)
			}
		})
	}
}

func TestCodingQuotaLaneRequiresExplicitProtocolBeforeIO(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--json"}, {"--source", "coding-plan"},
		{"--protocol", "qwen-coding-plan-v1", "--source", "coding-plan", "--source", "bl"},
		{"--protocol", "qwen-coding-plan-v1", "--source", "coding-plan", "--cache-only"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			m := commandManager(t, "success")
			// A nonexistent executable makes failure-before-exec observable
			// without a source-text assertion or a hidden substitute parser.
			m.cfg.Command = "missing-helper-must-not-execute"
			m.cfg.CommandArgs = args
			result, err := m.FetchQuota(context.Background(), quotaRequest())
			if err == nil || !strings.Contains(err.Error(), "coding_plan_readonly_configuration_required") || len(result.Groups) != 0 {
				t.Fatalf("legacy/default source reached subprocess: %#v %v", result, err)
			}
		})
	}
}

func TestCodingQuotaHelperBoundaryRejectsInvalidNativeCounts(t *testing.T) {
	for _, mode := range []string{"invalid-unit", "null-count", "wrong-remaining"} {
		t.Run(mode, func(t *testing.T) {
			m := managementCommandManager(t, mode)
			result, err := m.FetchQuota(context.Background(), quotaRequest())
			if err == nil || !strings.Contains(err.Error(), "invalid_quota") || len(result.Groups) != 0 || len(result.Summary) != 0 {
				t.Fatalf("invalid native contract became policy data: %#v %v", result, err)
			}
			resp, cards := managementUsage(t, m, `{}`)
			if resp.StatusCode != 200 || len(cards) == 0 || cards[0].QueryStatus != "failed" || cards[0].Error == nil || len(cards[0].Windows) != 0 {
				t.Fatalf("invalid native contract became a query success: %s", resp.Body)
			}
		})
	}
}

func TestCodingQuotaNonzeroExitNeverAccepted(t *testing.T) {
	m := managementCommandManager(t, "coding-nonzero")
	result, err := m.FetchQuota(context.Background(), quotaRequest())
	if err == nil || !strings.Contains(err.Error(), "quota command failed") || len(result.Groups) != 0 || len(result.Summary) != 0 {
		t.Fatalf("successful-looking JSON overrode nonzero exit: %#v %v", result, err)
	}
	resp, cards := managementUsage(t, m, `{}`)
	if resp.StatusCode != 200 || len(cards) == 0 || cards[0].QueryStatus != "failed" || cards[0].Error == nil || len(cards[0].Windows) != 0 {
		t.Fatalf("nonzero exit became query success: %s", resp.Body)
	}
}
