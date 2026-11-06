package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"

	"cpa-credential-qwen/internal/config"
)

func TestManagementRegisterReferenceEnvelope(t *testing.T) {
	m := NewManager(nil)
	raw, err := m.HandleCall(pluginabi.MethodManagementRegister, []byte(`{"Plugin":{"name":"cpa-credential-qwen"},"BasePath":"/v0/management","ResourceBasePath":"/v0/resource/plugins/cpa-credential-qwen"}`))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Routes    []struct{ Method, Path string }            `json:"routes"`
		Resources []struct{ Path, Menu, Description string } `json:"resources"`
	}
	decodeResult(t, raw, &result)
	if len(result.Resources) != 1 || result.Resources[0].Path != "/logo.svg" || result.Resources[0].Menu != "" {
		t.Fatalf("wrong resource envelope: %s", raw)
	}
	found := false
	for _, route := range result.Routes {
		if route.Method == "POST" && route.Path == "/plugins/cpa-credential-qwen/quota-usage" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing management route: %s", raw)
	}
	env := decodeEnv(t, raw)
	var wire map[string]json.RawMessage
	if json.Unmarshal(env.Result, &wire) != nil || wire["resources"] == nil || wire["routes"] == nil {
		t.Fatal("reference wire keys missing")
	}
	for _, method := range []string{pluginabi.MethodManagementRegister, pluginabi.MethodManagementHandle} {
		raw, _ = m.HandleCall(method, []byte(`{`))
		if decodeEnv(t, raw).OK {
			t.Fatal("accepted malformed request")
		}
	}
}

func TestManagementResourceEmbeddedHTML(t *testing.T) {
	// The process cwd has no resources directory: serving must use go:embed.
	t.Chdir(t.TempDir())
	m := NewManager(nil)
	raw, err := m.HandleCall(pluginabi.MethodManagementHandle, mustJSON(pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/v0/resource/plugins/cpa-credential-qwen/quota"}))
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.ManagementResponse
	decodeResult(t, raw, &resp)
	if resp.StatusCode != http.StatusOK || resp.Headers.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("bad HTML response: %#v", resp)
	}
	for _, marker := range []string{"套餐", "额度", "刷新", "剩余天数", "数据来自 CLI", "cli-proxy-auth", "enc::v1::", "/v0/management/plugins/cpa-credential-qwen/quota-usage"} {
		if !strings.Contains(string(resp.Body), marker) {
			t.Fatalf("missing %s", marker)
		}
	}
	for _, forbidden := range []string{"<script src=", "<link ", "dummy-config-key", "dummy-management-secret"} {
		if strings.Contains(string(resp.Body), forbidden) {
			t.Fatalf("unexpected asset or secret: %s", forbidden)
		}
	}
}

func managementCommandManager(t *testing.T, mode string) *Manager {
	t.Helper()
	m := commandManager(t, mode)
	entries := []pluginapi.HostAuthFileEntry{}
	for i, key := range m.cfg.APIKeys {
		digest := sha256.Sum256([]byte(key.Value))
		id := "qwen-key-" + hex.EncodeToString(digest[:])
		entries = append(entries, pluginapi.HostAuthFileEntry{ID: id, Name: id + ".json", Provider: ProviderID, AuthIndex: fmt.Sprintf("host-index-%d", i), Label: "Host label"})
	}
	entries = append(entries, pluginapi.HostAuthFileEntry{ID: "unowned", Provider: "other", AuthIndex: "foreign"})
	m.bridge = NewHostBridge(func(method string, _ []byte) ([]byte, error) {
		if method != pluginabi.MethodHostAuthList {
			t.Errorf("unexpected callback: %s", method)
		}
		return hostOK(hostAuthListResponse{Files: entries}), nil
	})
	return m
}

func managementUsage(t *testing.T, m *Manager, body string) (pluginapi.ManagementResponse, []quotaCard) {
	t.Helper()
	resp, err := m.HandleManagement(context.Background(), pluginapi.ManagementRequest{Method: "POST", Path: "/v0/management/plugins/cpa-credential-qwen/quota-usage", Body: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Cards []quotaCard `json:"cards"`
	}
	if json.Unmarshal(resp.Body, &result) != nil {
		t.Fatalf("invalid JSON: %s", resp.Body)
	}
	return resp, result.Cards
}

func assertLegacyQuotaCardUnbound(t *testing.T, card quotaCard) {
	t.Helper()
	if card.QueryStatus != "success" || card.AuthorityStatus != "needs_configuration" || card.Error == nil || !strings.Contains(*card.Error, "account_authority_unverified") {
		t.Errorf("legacy query acquired credential authority: %#v", card)
	}
	if len(card.Windows) != 0 || len(card.Metrics) != 0 || card.Plan != "" || card.PlanStatus != "" || card.PlanStart != "" || card.PlanEnd != "" || card.DaysLeft != nil || card.ObservedAt != "" {
		t.Errorf("account reading was attributed to a legacy credential card: %#v", card)
	}
}

func TestManagementQuotaUsageSuccessAndSelectedHostIndex(t *testing.T) {
	m := managementCommandManager(t, "coding")
	resp, cards := managementUsage(t, m, `{}`)
	if resp.StatusCode != 200 || len(cards) != len(m.cfg.APIKeys) || resp.Headers.Get("Content-Type") != "application/json" {
		t.Fatalf("wrong cards: %s", resp.Body)
	}
	card := cards[0]
	if card.AuthIndex != "host-index-0" || card.Label != accountLabel(m.cfg, m.cfg.APIKeys[0].Value, "Host label", 0) || card.QueryStatus != "success" || card.AuthorityStatus != "needs_configuration" || card.Error == nil || !strings.Contains(*card.Error, "account_authority_unverified") {
		t.Fatalf("wrong card: %#v", card)
	}
	if len(card.Windows) != 0 || len(card.Metrics) != 0 || card.Plan != "" || card.ObservedAt != "" {
		t.Fatalf("account query was attributed to selected key: %#v", card)
	}
	if card.PlanEnd != "" || card.DaysLeft != nil {
		t.Fatal("invented subscription period")
	}
	resp, cards = managementUsage(t, m, `{"auth_index":"host-index-0"}`)
	if resp.StatusCode != 200 || len(cards) != 1 || cards[0].AuthIndex != "host-index-0" {
		t.Fatalf("selection failed: %s", resp.Body)
	}
	assertLegacyQuotaCardUnbound(t, cards[0])
	for _, key := range m.cfg.APIKeys {
		if strings.Contains(string(resp.Body), key.Value) {
			t.Fatal("key leaked")
		}
	}
}

func TestManagementQuotaUsageCLIPlanPeriodAndMetricsOnly(t *testing.T) {
	// Old Token Plan subscription periods and cash-like metrics no longer
	// satisfy the Coding Plan credential contract. Keep both regressions.
	for _, mode := range []string{"period", "metrics-only"} {
		m := managementCommandManager(t, mode)
		resp, cards := managementUsage(t, m, `{}`)
		if resp.StatusCode != 200 || len(cards) == 0 {
			t.Fatalf("missing error card: %s", resp.Body)
		}
		card := cards[0]
		if card.Error == nil || !strings.Contains(*card.Error, "coding_plan_helper_protocol_required") || card.PlanStart != "" || card.PlanEnd != "" || card.DaysLeft != nil || len(card.Windows) != 0 || len(card.Metrics) != 0 {
			t.Fatalf("legacy product fabricated a Coding Plan reading: %s", resp.Body)
		}
	}
}

func TestManagementQuotaUsageFailuresAreCardsNotFakeReadings(t *testing.T) {
	for mode, want := range map[string]string{"failure": "ConsoleNeedLogin: CLI says login required", "zero-error": "ConsoleNeedLogin: CLI says login required", "empty": "coding_plan_helper_protocol_required", "garbage": "CLI garbage diagnostic"} {
		t.Run(mode, func(t *testing.T) {
			m := managementCommandManager(t, mode)
			resp, cards := managementUsage(t, m, `{}`)
			if resp.StatusCode != 200 || len(cards) == 0 {
				t.Fatalf("lost error card: %s", resp.Body)
			}
			for _, card := range cards {
				if card.Error == nil || !strings.Contains(*card.Error, want) || card.Plan != "" || card.PlanEnd != "" || card.ObservedAt != "" || card.DaysLeft != nil || len(card.Windows) != 0 || len(card.Metrics) != 0 {
					t.Fatalf("fabricated reading: %#v", card)
				}
			}
		})
	}
}

func TestManagementQuotaUsageUnknownEmptyAndInvalidRequests(t *testing.T) {
	m := managementCommandManager(t, "success")
	m.cfg.Command = "missing-command-must-not-run"
	for _, index := range []string{"unknown", "foreign", "qwen-key-not-a-host-index"} {
		resp, _ := managementUsage(t, m, fmt.Sprintf(`{"auth_index":%q}`, index))
		if resp.StatusCode != 404 || string(resp.Body) != `{"error":"unknown auth_index"}` {
			t.Fatalf("unknown credential accepted: %s", resp.Body)
		}
	}
	resp, _ := managementUsage(t, m, `{`)
	if resp.StatusCode != 400 {
		t.Fatal("accepted invalid body")
	}
	m.cfg.APIKeys = nil
	resp, cards := managementUsage(t, m, `{}`)
	if resp.StatusCode != 200 || len(cards) != 0 || string(resp.Body) != `{"cards":[]}` {
		t.Fatalf("empty credentials: %s", resp.Body)
	}
	resp, err := m.HandleManagement(context.Background(), pluginapi.ManagementRequest{Method: "GET", Path: "/unknown"})
	if err != nil || resp.StatusCode != 404 {
		t.Fatalf("unexpected route result: %#v %v", resp, err)
	}
}

func TestManagementQuotaCredentialsHostFailuresAreHonest(t *testing.T) {
	m := commandManager(t, "success")
	resp, _ := managementUsage(t, m, `{}`)
	if resp.StatusCode != 502 {
		t.Fatal("invented host indexes")
	}
	m.bridge = NewHostBridge(func(string, []byte) ([]byte, error) {
		return hostOK(hostAuthListResponse{Files: []pluginapi.HostAuthFileEntry{}}), nil
	})
	resp, _ = managementUsage(t, m, `{}`)
	if resp.StatusCode != 502 {
		t.Fatal("hid missing configured credentials")
	}
	key := m.cfg.APIKeys[0]
	digest := sha256.Sum256([]byte(key.Value))
	m.bridge = NewHostBridge(func(string, []byte) ([]byte, error) {
		return hostOK(hostAuthListResponse{Files: []pluginapi.HostAuthFileEntry{{Provider: ProviderID, Name: "qwen-key-" + hex.EncodeToString(digest[:]) + ".json"}}}), nil
	})
	resp, _ = managementUsage(t, m, `{}`)
	if resp.StatusCode != 502 {
		t.Fatal("invented unavailable runtime index")
	}
}

func TestManagementRemainingDaysCeiling(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	for _, sample := range []struct {
		name  string
		delta time.Duration
		want  int
	}{
		{"exact", 10 * 24 * time.Hour, 10}, {"partial", 10*24*time.Hour + time.Second, 11}, {"subday", time.Second, 1}, {"now", 0, 0}, {"expired_subday", -time.Second, 0}, {"expired", -25 * time.Hour, -1},
	} {
		t.Run(sample.name, func(t *testing.T) {
			got := daysUntil(now.Add(sample.delta).Format(time.RFC3339), now)
			if got == nil || *got != sample.want {
				t.Fatalf("got %v want %d", got, sample.want)
			}
		})
	}
	for _, timestamp := range []string{"", "garbage"} {
		if daysUntil(timestamp, now) != nil {
			t.Fatal("invented days")
		}
	}
}

func TestManagementPeriodMappingAndMetricsOnly(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	var raw commandQuota
	if err := json.Unmarshal([]byte(quotaFixture), &raw); err != nil {
		t.Fatal(err)
	}
	raw.PlanStart = "2026-09-17T18:11:24+08:00"
	raw.PlanEnd = "2027-09-18T00:00:00+08:00"
	staleDays := 999
	raw.DaysLeft = &staleDays
	card := quotaCard{Windows: []quotaWindow{}, Metrics: []pluginapi.QuotaMetric{}}
	populateQuotaCard(&card, raw, providerConfig(t), now)
	if card.PlanStart != raw.PlanStart || card.PlanEnd != raw.PlanEnd || card.DaysLeft == nil || *card.DaysLeft != 345 || card.Windows[0].ResetsInDays == nil || *card.Windows[0].ResetsInDays != 10 {
		t.Fatalf("period mapping failed: %#v", card)
	}
	raw.PlanStart, raw.PlanEnd = "invalid", "invalid"
	raw.Windows = nil
	card = quotaCard{Windows: []quotaWindow{}, Metrics: []pluginapi.QuotaMetric{}}
	populateQuotaCard(&card, raw, providerConfig(t), now)
	if card.PlanStart != "" || card.PlanEnd != "" || card.DaysLeft != nil || len(card.Windows) != 0 || len(card.Metrics) != 1 {
		t.Fatalf("invented date or metrics-only windows: %#v", card)
	}
}

func TestManagementNoSecretsInFailuresAndSuccessFields(t *testing.T) {
	m := managementCommandManager(t, "secret")
	resp, cards := managementUsage(t, m, `{}`)
	if len(cards) == 0 || cards[0].Error == nil {
		t.Fatal("missing error")
	}
	for _, secret := range []string{"dummy-config-key", "private-cookie", "private-token", "sk-sp-private-key"} {
		if strings.Contains(string(resp.Body), secret) {
			t.Fatalf("secret leaked: %s", resp.Body)
		}
	}
	var raw commandQuota
	if err := json.Unmarshal([]byte(quotaFixture), &raw); err != nil {
		t.Fatal(err)
	}
	raw.Plan, raw.PlanStatus, raw.ObservedAt = "dummy-config-key", "sk-sp-private-key", "token=private-token"
	raw.Windows[0].Window = "dummy-config-key"
	raw.Metrics[0].Key, raw.Metrics[0].Label, raw.Metrics[0].Unit, raw.Metrics[0].Currency = "dummy-config-key", "dummy-config-key", "dummy-config-key", "dummy-config-key"
	card := quotaCard{}
	populateQuotaCard(&card, raw, m.cfg, time.Now())
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"dummy-config-key", "sk-sp-private-key", "private-token"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("success field leaked: %s", encoded)
		}
	}
}

func TestManagementLegacyQuotaBulkAndSingleNeverAttributeCLIReading(t *testing.T) {
	for _, transport := range []string{"live-cli", "cache-only"} {
		t.Run(transport, func(t *testing.T) {
			m := managementCommandManager(t, "coding")
			if transport == "cache-only" {
				m.cfg.CommandArgs = []string{
					"-test.run=^TestCLIQuotaFixtureProcess$", "--", "--cli-quota-fixture", "cache",
					"--source=coding-plan", "--protocol=qwen-coding-plan-v1", "--cache-only=true",
					"--cache-file=C:/synthetic cache/coding quota.json", "--max-age", "45m",
				}
			}
			m.cfg.APIKeys = append(m.cfg.APIKeys, config.APIKey{Value: "dummy-second-key", Name: "Second Qwen"})
			entries := []pluginapi.HostAuthFileEntry{}
			for i, key := range m.cfg.APIKeys {
				digest := sha256.Sum256([]byte(key.Value))
				entries = append(entries, pluginapi.HostAuthFileEntry{Type: ProviderID, Name: "qwen-key-" + hex.EncodeToString(digest[:]) + ".json", AuthIndex: fmt.Sprintf("index-%d", i)})
			}
			m.bridge = NewHostBridge(func(string, []byte) ([]byte, error) { return hostOK(hostAuthListResponse{Files: entries}), nil })
			for _, selection := range []struct {
				name, body string
				indexes    []string
			}{
				{"bulk", `{}`, []string{"index-0", "index-1"}},
				{"single-first", `{"auth_index":"index-0"}`, []string{"index-0"}},
				{"single-second", `{"auth_index":"index-1"}`, []string{"index-1"}},
			} {
				t.Run(selection.name, func(t *testing.T) {
					resp, cards := managementUsage(t, m, selection.body)
					if resp.StatusCode != 200 || len(cards) != len(selection.indexes) {
						t.Fatalf("lost selected credentials: %s", resp.Body)
					}
					for i, card := range cards {
						if card.AuthIndex != selection.indexes[i] || card.AuthIndex == "index-1" && card.Label != "Second Qwen" {
							t.Fatalf("wrong selection: %s", resp.Body)
						}
						assertLegacyQuotaCardUnbound(t, card)
					}
				})
			}
		})
	}
}
