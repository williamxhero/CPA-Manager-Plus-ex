package officialbalance

import (
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestBalanceTextByResourceUsesTheSingleSavedAmount(t *testing.T) {
	records := []store.OfficialBalanceRecord{
		{Config: `{"targets":[{"resource_id":"14e6284d-1111-4111-8111-111111111111"},{"resource_id":"b341a945-1111-4111-8111-111111111111"}]}`, LastSuccess: `{"queryStatus":"success","amountDecimal":"46.92","currency":"USD"}`},
		{Config: `{"targets":[{"resource_id":"286a81e0-1111-4111-8111-111111111111"}]}`, LastSuccess: `{"queryStatus":"success","amountDecimal":"53.50672686","currency":"USD"}`},
		{Config: `{"targets":[{"resource_id":"shared-1"}]}`, LastSuccess: `{"queryStatus":"success","amountDecimal":"1.00","currency":"USD"}`},
		{Config: `{"targets":[{"resource_id":"shared-1"}]}`, LastSuccess: `{"queryStatus":"success","amountDecimal":"2.00","currency":"USD"}`},
	}
	got := balanceTextByResource(records)
	if got["14e6284d-1111-4111-8111-111111111111"] != "USD 46.92" || got["286a81e0-1111-4111-8111-111111111111"] != "USD 53.50672686" {
		t.Fatalf("saved amounts = %#v", got)
	}
	if _, ok := got["shared-1"]; ok {
		t.Fatal("ambiguous resource kept a balance")
	}
}

func TestRestoreCachedBalancesReplacesOnlyKnownEmpty(t *testing.T) {
	texts := map[string]string{
		"claude:14e6284d-1111-4111-8111-111111111111": "—",
		"codex:5b13b894-1111-4111-8111-111111111111":  "—",
		"claude:1a652329-1111-4111-8111-111111111111": "USD 4.56",
	}
	records := []store.OfficialBalanceRecord{
		{Config: `{"targets":[{"resource_id":"14e6284d-1111-4111-8111-111111111111"}]}`, LastSuccess: `{"queryStatus":"success","amountDecimal":"46.92","currency":"USD"}`},
	}
	saved := balanceTextByResource(records)
	for key, value := range texts {
		if value != knownEmptyBalance {
			continue
		}
		match := balanceKeyID.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		if text, ok := saved[strings.ToLower(match[1])]; ok {
			texts[key] = text
		}
	}
	if texts["claude:14e6284d-1111-4111-8111-111111111111"] != "USD 46.92" {
		t.Fatalf("hidden balance = %q", texts["claude:14e6284d-1111-4111-8111-111111111111"])
	}
	if texts["codex:5b13b894-1111-4111-8111-111111111111"] != "—" || texts["claude:1a652329-1111-4111-8111-111111111111"] != "USD 4.56" {
		t.Fatalf("other rows changed: %#v", texts)
	}
	if observationDisplayText(balance.Observation{QueryStatus: "success", NativeQuota: &balance.NewAPINativeQuota{State: "finite"}}) != "" {
		t.Fatal("native quota without money became a balance")
	}
}
