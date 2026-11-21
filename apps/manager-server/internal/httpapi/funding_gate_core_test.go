package httpapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

type fundingGateRow struct {
	Owner   string
	Reason  string
	Targets []rc.Target
	Funding struct {
		Source       string
		ObservedAtMS int64
	}
}

func TestOfficialFundingDeepSeekPausesOnlyExactKeyWithoutOptIn(t *testing.T) {
	var fallbackSends atomic.Int64
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fallbackSends.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"independent-funding"},"finish_reason":"stop"}]}`)
	}))
	defer fallback.Close()
	extra := fmt.Sprintf("    - name: surviving-route\n      priority: -100\n      base-url: %s/v1\n      models: [{name: controlled-model}]\n      keys: [{api-key: fallback-a, proxy-url: direct}, {api-key: fallback-b, proxy-url: direct}]\n", fallback.URL)
	f := newPolicyFixture(t, false, true, false, extra)
	if f.state.Enabled || len(f.state.Targets) != 2 {
		t.Fatalf("requires current issued money-provider with legacy opt-in OFF: %+v", f.state)
	}
	var document atomic.Value
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(policyMoneyNetwork(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/user/balance" || r.Header.Get("Authorization") != "Bearer MONEY-SYNTHETIC-INFERENCE" {
			t.Error("query did not authenticate using the exact runtime key on the official read")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, document.Load().(string))
	})))
	query := func(available bool, total, grants, toppedUp string) ob.Job {
		t.Helper()
		document.Store(fmt.Sprintf(`{"is_available":%t,"balance_infos":[{"currency":"CNY","total_balance":%q,"granted_balance":%q,"topped_up_balance":%q}]}`, available, total, grants, toppedUp))
		job := f.query("unused")
		awaitProviderPolicyEvent(t, f.db, job.ID)
		return job
	}
	ledger := func() []fundingGateRow {
		t.Helper()
		var state struct{ Gates []fundingGateRow }
		f.coreRequest("GET", "/v8/management/inference-gates", nil, 200, &state)
		return state.Gates
	}
	send := func() {
		t.Helper()
		req, err := http.NewRequest("POST", f.core.base+"/v1/chat/completions", bytes.NewBufferString(`{"model":"controlled-model","messages":[{"role":"user","content":"route check"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer MONEY-CLIENT")
		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 200 || !bytes.Contains(body, []byte("independent-funding")) {
			t.Fatalf("healthy independent route did not complete inference: HTTP %d %s error=%v", res.StatusCode, body, err)
		}
	}
	f.startRuntime()
	query(true, "0.0001", "0.0501", "-0.05") // net positive despite display rounding and negative paid credit.
	if rows := ledger(); len(rows) != 0 {
		t.Fatalf("positive grants/total applied an exhaustion gate: %+v", rows)
	}
	zero := query(false, "0", "0", "0")
	if rows := ledger(); len(rows) != 1 || rows[0].Reason != "funding_exhausted" || len(rows[0].Targets) != 1 || rows[0].Targets[0] != f.state.Targets[1] || rows[0].Funding.Source != "deepseek" || rows[0].Funding.ObservedAtMS != zero.Status.Attempt.ObservedAtMS {
		t.Fatalf("confirmed zero from the exact runtime key must independently pause its member without #12 opt-in: %+v", rows)
	}
	send()
	if f.core.denyDeepSeekConnects.Load() != 0 || fallbackSends.Load() != 1 {
		t.Fatal("wallet-depleted member dispatched or healthy fallback was lost")
	}
	query(true, "0.0001", "0", "0.0001")
	if rows := ledger(); len(rows) != 0 {
		t.Fatalf("fresh exact positive did not recover only its own funding gate: %+v", rows)
	}
	send()
	if f.core.denyDeepSeekConnects.Load() == 0 || fallbackSends.Load() != 2 {
		t.Fatal("new positive evidence did not readmit the same member before fallback")
	}
}
