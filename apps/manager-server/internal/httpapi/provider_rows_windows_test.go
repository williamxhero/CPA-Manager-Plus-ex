package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Seed only through public APIs with a controlled external balance transport.
// The ordinary production main then owns SQLite and serves both provider rows.
func TestProviderRowSummaryMainReadOnlyAcrossRestart(t *testing.T) {
	if os.Getenv("PROVIDER82_MANAGER_BINARY") == "" {
		t.Skip("explicit built Manager artifact required")
	}
	f := newProviderFundingFixture(t)
	var fail atomic.Bool
	var queries atomic.Int32
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"0.123456789012345678","granted_balance":"0","topped_up_balance":"0"}]}`)
	}))
	a := f.providerID
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
	f.enableMoney(false)
	fail.Store(true)
	f.refreshMoney("FUNDING-SYNTHETIC-A1", 1)
	// Discover B using producer-issued IDs, not its name or list position.
	inv := f.api("GET", "/usage-service/resource-capabilities?inventory=1&bindings=1", nil, 200)
	var envelope struct{ Negotiated rc.Negotiated }
	raw, _ := json.Marshal(inv)
	if json.Unmarshal(raw, &envelope) != nil || envelope.Negotiated.Snapshot == nil {
		t.Fatal("issued inventory unavailable")
	}
	for _, row := range envelope.Negotiated.Snapshot.Resources {
		if row.Kind == "provider" && row.ResourceID != a {
			f.providerID = row.ResourceID
		}
	}
	b := f.providerID
	if a == b {
		t.Fatal("second provider unavailable")
	}
	for _, row := range envelope.Negotiated.Snapshot.Resources {
		if row.Kind == "credential" && row.ParentID == b {
			f.members["FUNDING-SYNTHETIC-B1"] = ob.Binding{Scope: envelope.Negotiated.Snapshot.Scope, Targets: []rc.Target{row.Target}}
		}
	}
	f.enableMoney(false) // explicit intent, but B has no amount observation
	f.shutdown()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	stop := f.startRuntimeMain("rows-first", "", "", "")
	defer func() { stop() }()
	readRows := func() []map[string]any {
		t.Helper()
		var states []map[string]any
		for _, id := range []string{a, b} {
			f.providerID = id
			state := f.api("GET", f.policyPath(), nil, 200)
			if state["providerId"] != id || state["enabled"] != false || state["generation"] != float64(1) || state["source"] != "explicit" || state["bindingCurrent"] != true {
				t.Fatalf("row identity/intent was not durable: %+v", state)
			}
			states = append(states, state)
		}
		sources := states[0]["sources"].([]any)
		if len(sources) != 2 {
			t.Fatal("A lost a member")
		}
		stale := false
		for _, raw := range sources {
			source := raw.(map[string]any)
			stale = stale || source["stale"] == true
			if source["lastSuccess"].(map[string]any)["balances"].([]any)[0].(map[string]any)["total"] != "0.123456789012345678" {
				t.Fatal("A lost native precision/lastSuccess")
			}
		}
		if !stale {
			t.Fatal("A failed read did not remain stale")
		}
		for _, raw := range states[1]["sources"].([]any) {
			if raw.(map[string]any)["lastSuccess"] != nil {
				t.Fatal("B acquired A's amount")
			}
		}
		return states
	}
	readRows()
	stop()
	stop = f.startRuntimeMain("rows-restart", "", "", "")
	states := readRows()
	req, _ := http.NewRequest("GET", f.apiBase+f.policyPath(), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("unauthenticated summary status=%d", res.StatusCode)
	}
	if fixture := os.Getenv("PROVIDER82_ROWS_LIVE_UI"); fixture != "" {
		doc, _ := json.Marshal(map[string]any{"base": f.apiBase, "managementKey": testutil.AdminKey, "states": states})
		if err := os.WriteFile(fixture, doc, 0600); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(60 * time.Second); ; {
			if _, err := os.Stat(fixture + ".stop"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("row UI handshake timeout")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	readRows()
	if f.operations.Load() != 0 || len(f.ledger()["gates"].([]any)) != 0 {
		t.Fatal("summary reads wrote a gate")
	}
	doc, _ := json.MarshalIndent(states, "", "  ")
	if err := os.WriteFile(filepath.Join(os.Getenv("PROVIDER82_PROCESS_LOG_DIR"), "row-statuses.json"), doc, 0600); err != nil {
		t.Fatal(err)
	}
}
