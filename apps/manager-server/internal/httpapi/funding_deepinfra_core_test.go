package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

func TestOfficialFundingDeepInfraOnlyVerifiedSharedWalletMembers(t *testing.T) {
	var healthy atomic.Int64
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			w.WriteHeader(404)
			return
		}
		healthy.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"healthy"},"finish_reason":"stop"}]}`)
	}))
	defer fallback.Close()
	routes := ""
	for _, label := range []string{"A", "D", "B", "U", "P", "W"} {
		routes += fmt.Sprintf("    - name: wallet-%s\n      base-url: https://api.deepinfra.com/v1/openai\n      models: [{name: wallet-model-%s}]\n      keys: [{api-key: wallet-fixture-%s}]\n", label, label, label)
	}
	routes += fmt.Sprintf("    - name: surviving-route\n      priority: -100\n      base-url: %s/v1\n      models: [{name: wallet-model-A}, {name: wallet-model-D}, {name: wallet-model-B}, {name: wallet-model-U}, {name: wallet-model-P}, {name: wallet-model-W}]\n      keys: [{api-key: fallback-a, proxy-url: direct}, {api-key: fallback-b, proxy-url: direct}]\n", fallback.URL)
	// No request should go out through the synthetic eligible DeepInfra routes.
	// The fixture's process proxy denies all WAN egress, including false admits.
	f := newPolicyFixtureWithSetup(t, false, true, false, false, routes)
	var stage atomic.Int32
	var pausedStamp atomic.Int64
	var accountCalls, legacyCalls atomic.Int32
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(policyFundingNetwork(t, "api.deepinfra.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer wallet-fixture-")
		root, stripe, suspended, reason, credits := "SYNTHETIC-PRIVATE-ROOT", "0", true, `"balance"`, "[]"
		if key == "B" {
			root, stripe, suspended, reason = "SYNTHETIC-OTHER-ROOT", "-0.0001", false, "null"
		}
		if key == "U" {
			credits = `[{"remaining_cents":0.0001,"expired":false,"expires_ts":null,"covers_everything":false}]`
		}
		if stage.Load() == 1 && key == "A" {
			root, stripe, suspended, reason = "SYNTHETIC-OTHER-ROOT", "-0.0001", false, "null"
		}
		checkedAt := time.Now().Unix()
		if key == "A" && stage.Load() == 0 {
			pausedStamp.CompareAndSwap(0, checkedAt)
		}
		if stage.Load() >= 2 && (key == "A" || key == "D") {
			stripe, suspended, reason = "-0.0001", false, "null"
		}
		if stage.Load() == 2 && key == "A" {
			// A fresh HTTP request can still serve an older cached provider state.
			checkedAt = pausedStamp.Load() - 1
		}
		check := fmt.Sprintf(`{"suspended":%t,"suspend_reason":%s,"stripe_balance":%s,"recent":0,"last_checked":%d,"scoped_credits":%s,"overdue_invoices":0}`, suspended, reason, stripe, checkedAt, credits)
		switch r.URL.Path {
		case "/v1/me":
			accountCalls.Add(1)
			if r.Method != "GET" || r.URL.RawQuery != "checklist=true" || (key != "A" && key != "D" && key != "B" && key != "U" && key != "P") {
				t.Error("invalid DeepInfra authority query")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if key == "P" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprintf(w, `{"root_uid":%q,"checklist":%s}`, root, check)
		case "/payment/checklist":
			legacyCalls.Add(1)
			fmt.Fprint(w, check)
		default:
			t.Error("unexpected DeepInfra read")
			w.WriteHeader(404)
		}
	})))
	var n struct{ Negotiated rc.Negotiated }
	f.api(http.MethodGet, "/usage-service/resource-capabilities?inventory=1&bindings=1", nil, 200, &n)
	// Read the Core-written resource marker for each synthetic key, then require
	// that exact credential in the real management inventory.
	members := controlledDeepInfraPeers(t, f, n.Negotiated)
	if len(members) != 6 {
		t.Fatalf("failed to select six distinct issued peers: %v", members)
	}
	jobs := map[string]ob.Query{}
	for label, member := range members {
		var cfg ob.Status
		b := ob.Binding{Scope: n.Negotiated.Snapshot.Scope, Targets: []rc.Target{member}}
		secret := "wallet-fixture-" + label
		if label == "W" {
			secret = "wallet-fixture-A" // Authorized payer response for another runtime credential.
		}
		f.api(http.MethodPut, balancePath, ob.Update{Config: ob.Config{Binding: b, Adapter: "deepinfra", Endpoint: "https://api.deepinfra.com/payment/checklist", PrivateTargets: []string{"127.0.0.1"}}, Secret: secret}, 200, &cfg)
		jobs[label] = ob.Query{Binding: b, Version: cfg.Version}
	}
	f.startRuntime()
	query := func(label string) {
		t.Helper()
		var job ob.Job
		f.api(http.MethodPost, balancePath, jobs[label], 202, &job)
		select {
		case id := <-f.completed:
			if id != job.ID {
				t.Fatal("unexpected completion")
			}
		case <-time.After(15 * time.Second):
			t.Fatal("no official completion")
		}
		var done ob.Job
		f.api(http.MethodGet, balancePath+"?job="+url.QueryEscape(job.ID), nil, 200, &done)
		if done.Status == nil || done.Status.Attempt == nil || done.Status.Attempt.QueryStatus != "success" {
			t.Fatalf("expected separately authenticated wallet read: %+v", done)
		}
		doc, _ := json.Marshal(done)
		if bytes.Contains(doc, []byte("SYNTHETIC-")) || bytes.Contains(doc, []byte("privateDeepInfraFunding")) || bytes.Contains(doc, []byte(`"payer"`)) {
			t.Fatal("private payer evidence escaped official job readback")
		}
		awaitProviderPolicyEvent(t, f.db, job.ID)
	}
	assert := func(labels ...string) {
		t.Helper()
		var payload struct{ Gates []fundingGateRow }
		f.coreRequest("GET", "/v8/management/inference-gates", nil, 200, &payload)
		if len(labels) != len(payload.Gates) {
			t.Fatalf("wallet gate scopes: want %v; got %+v", labels, payload.Gates)
		}
		for _, label := range labels {
			found := false
			for _, g := range payload.Gates {
				found = found || (len(g.Targets) == 1 && g.Targets[0] == members[label] && g.Funding.Source == "deepinfra" && g.Reason == "funding_exhausted")
			}
			if !found {
				t.Fatalf("missing confirmed wallet dependency for distinct issued member %s: %+v", label, payload.Gates)
			}
		}
	}
	query("A")
	assert("A") // No host/name-based propagation.
	query("D")
	query("B")
	query("U")
	query("P")
	query("W")
	assert("A", "D")
	if accountCalls.Load() != 6 || legacyCalls.Load() != 1 {
		t.Fatalf("expected one scoped read per target with only the denied scope using display fallback: account=%d legacy=%d", accountCalls.Load(), legacyCalls.Load())
	}
	send := func(label string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, f.core.base+"/v1/chat/completions", bytes.NewBufferString(fmt.Sprintf(`{"model":"wallet-model-%s","messages":[{"role":"user","content":"route check"}]}`, label)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer MONEY-CLIENT")
		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("wallet dependency did not leave healthy dispatch: %v %v", res, err)
		}
		res.Body.Close()
	}
	send("A")
	send("D")
	if healthy.Load() != 2 || f.core.denyDeepInfraConnects.Load() != 0 {
		t.Fatal("wallet-depleted members dispatched, or independent fallback was lost")
	}
	send("B")
	if healthy.Load() != 3 || f.core.denyDeepInfraConnects.Load() == 0 {
		t.Fatal("independently funded member did not dispatch before fallback")
	}
	stage.Store(1)
	query("A")
	assert("A", "D") // Different payer cannot clear existing wallet evidence.
	stage.Store(2)
	query("A")
	assert("A", "D") // A newer request with older provider state cannot recover.
	stage.Store(3)
	query("A")
	assert("D")
	query("D")
	assert()
	initial := f.core.denyDeepInfraConnects.Load()
	send("A")
	if f.core.denyDeepInfraConnects.Load() == initial {
		t.Fatal("fresh same-payer funding did not readmit the released member")
	}
}

func controlledDeepInfraPeers(t *testing.T, f *policyUnsentFixture, n rc.Negotiated) map[string]rc.Target {
	t.Helper()
	if n.Snapshot == nil || !n.Snapshot.Complete {
		t.Fatal("missing actual issued Core inventory")
	}
	var saved struct {
		API struct {
			Compat []struct {
				Keys []struct {
					Key        string `json:"api-key"`
					ResourceID string `json:"resource-id"`
				} `json:"keys"`
			} `json:"openai-compatibility"`
		} `json:"api-keys"`
	}
	f.coreRequest("GET", "/v8/management/config", nil, 200, &saved)
	result := map[string]rc.Target{}
	for _, entry := range saved.API.Compat {
		for _, key := range entry.Keys {
			label, ok := strings.CutPrefix(key.Key, "wallet-fixture-")
			if !ok {
				continue
			}
			for _, row := range n.Snapshot.Resources {
				if row.ResourceID == key.ResourceID && row.Kind == "credential" && row.ParentID != "" {
					result[label] = row.Target
				}
			}
		}
	}
	return result
}
