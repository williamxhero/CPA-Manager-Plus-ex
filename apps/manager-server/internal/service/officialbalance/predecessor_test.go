package officialbalance

import (
	"encoding/json"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestPredecessorRecordUsesTheSingleRemainingBrowserSession(t *testing.T) {
	request := Binding{Scope: testScope(), Targets: []rc.Target{target("provider", "p1"), target("credential", "c2")}}
	got, ok := predecessorRecord(request, []store.OfficialBalanceRecord{
		browserRecord("https://api-top.com", target("provider", "p1"), target("old", "o1")),
		browserRecord("https://code28.ccwu.cc", target("provider", "x"), target("credential", "y")),
	})
	if !ok {
		t.Fatal("expected the api-top predecessor")
	}
	var cfg Config
	if json.Unmarshal([]byte(got.Config), &cfg) != nil || cfg.BrowserSession.Origin != "https://api-top.com" {
		t.Fatalf("origin mismatch %+v", cfg.BrowserSession)
	}
}

func TestPredecessorRecordKeepsSavedBalanceAfterProviderTargetChanges(t *testing.T) {
	request := Binding{Scope: testScope(), Targets: []rc.Target{target("provider-new", "p2"), target("credential", "c1")}}
	cfg := Config{
		Binding:  Binding{Scope: testScope(), Targets: []rc.Target{target("provider-old", "p1"), target("credential", "c1")}},
		Adapter:  balance.LaysoBillingWebV1,
		Endpoint: balance.LaysoBillingPage,
	}
	raw, _ := json.Marshal(cfg)
	got, ok := predecessorRecord(request, []store.OfficialBalanceRecord{{
		Config:      string(raw),
		LastSuccess: `{"queryStatus":"success","amountDecimal":"46.92","currency":"USD"}`,
	}})
	if !ok {
		t.Fatal("expected the saved layso balance")
	}
	var obs balance.Observation
	if json.Unmarshal([]byte(got.LastSuccess), &obs) != nil || observationDisplayText(obs) != "USD 46.92" {
		t.Fatalf("display text = %q", observationDisplayText(obs))
	}
}

func TestPredecessorRecordRejectsAmbiguousOrIntactBindings(t *testing.T) {
	request := Binding{Scope: testScope(), Targets: []rc.Target{target("provider", "p1"), target("credential", "c2")}}
	if _, ok := predecessorRecord(request, []store.OfficialBalanceRecord{
		browserRecord("https://api-top.com", target("provider", "p1"), target("old", "o1")),
		browserRecord("https://code28.ccwu.cc", target("credential", "c2"), target("old", "o2")),
	}); ok {
		t.Fatal("ambiguous predecessor")
	}
	if _, ok := predecessorRecord(request, []store.OfficialBalanceRecord{
		browserRecord("https://api-top.com", target("provider", "p1"), target("credential", "c2")),
	}); ok {
		t.Fatal("intact binding is not a predecessor")
	}
}

func testScope() rc.Scope {
	return rc.Scope{InstanceID: "instance", InstanceIncarnation: "inc"}
}

func browserRecord(origin string, targets ...rc.Target) store.OfficialBalanceRecord {
	cfg := Config{
		Binding:        Binding{Scope: testScope(), Targets: targets},
		Adapter:        balance.NewAPIBrowserSessionV1,
		BrowserSession: &balance.BrowserSessionConfig{Origin: origin},
	}
	raw, _ := json.Marshal(cfg)
	return store.OfficialBalanceRecord{Config: string(raw), LastSuccess: `{"queryStatus":"success"}`}
}
