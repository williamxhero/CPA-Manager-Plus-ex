package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestProviderPolicyOfficialMoneyPauseAndFreshRecovery(t *testing.T) {
	base := moneyPolicyCore(t)
	cfg := testutil.NewConfig(t)
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testutil.EnsureAdminCredential(t, db)
	if err := db.SaveManagerConfig(context.Background(), store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: base, ManagementKey: "MONEY-CORE-MANAGEMENT"}}); err != nil {
		t.Fatal(err)
	}
	manager := New(cfg, db, collector.NewManager(cfg, db))
	h := manager.Handler()
	api := func(method, path string, q any, want int) map[string]any {
		t.Helper()
		body := ""
		if q != nil {
			raw, _ := json.Marshal(q)
			body = string(raw)
		}
		r := testutil.Request(t, h, method, path, body, testutil.AdminKey)
		testutil.RequireStatus(t, r, want)
		var out map[string]any
		testutil.DecodeJSON(t, r, &out)
		return out
	}
	inv := manager.AppContext().ResourceCapabilityService.Inventory(context.Background(), true)
	if inv.Snapshot == nil {
		t.Fatal("issued inventory unavailable")
	}
	id := ""
	for _, row := range inv.Snapshot.Resources {
		if row.Kind == "provider" {
			id = row.ResourceID
		}
	}
	if id == "" {
		t.Fatal("provider missing")
	}
	path := "/usage-service/provider-policy?providerId=" + id
	state := api("GET", path, nil, 200)
	query := map[string]any{"scope": state["scope"], "targets": state["targets"], "version": 0, "adapter": "deepseek", "endpoint": "https://api.deepseek.com/user/balance", "privateTargets": []string{"127.0.0.1"}, "secret": "MONEY-SYNTHETIC-INFERENCE"}
	api("PUT", balancePath, query, 200)
	var money atomic.Value
	money.Store("0.1")
	var sends atomic.Int64
	network := policyMoneyNetwork(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.URL.Path != "/user/balance" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer MONEY-SYNTHETIC-INFERENCE" {
			t.Error("invalid actual balance request")
			w.WriteHeader(401)
			return
		}
		fmt.Fprintf(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":%q,"granted_balance":"0","topped_up_balance":%q}]}`, money.Load().(string), money.Load().(string))
	}))
	manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(network)
	stopPolicy := startProviderPolicyRuntime(t, manager)
	defer shutdownProviderPolicyManager(t, manager, stopPolicy)
	enable := map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": id, "expectedGeneration": 0, "enabled": true}
	state = api("PUT", "/usage-service/provider-policy", enable, 200)
	if state["enabled"] != true || state["ownPaused"] == true {
		t.Fatal("saving ON applied cached/unqueried evidence")
	}
	refresh := func(wantPaused bool) map[string]any {
		t.Helper()
		job := api("POST", balancePath, map[string]any{"scope": state["scope"], "targets": state["targets"], "version": 1}, 202)
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			j := api("GET", balancePath+"?job="+job["id"].(string), nil, 200)
			if j["state"] == "complete" { // Completion subscriber runs after durable publication.
				awaitProviderPolicyEvent(t, db, job["id"].(string))
				for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
					s := api("GET", path, nil, 200)
					if s["ownPaused"] == wantPaused && (s["state"] == "paused" || s["state"] == "observing") {
						return s
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("balance/policy completion did not settle")
		return nil
	}
	revision := func() uint64 {
		t.Helper()
		req, _ := http.NewRequest("GET", base+"/v8/management/inference-gates", nil)
		req.Header.Set("Authorization", "Bearer MONEY-CORE-MANAGEMENT")
		res, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var ledger struct {
			GateRevision uint64 `json:"gateRevision"`
		}
		if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&ledger) != nil {
			t.Fatal("actual gate ledger unavailable")
		}
		return ledger.GateRevision
	}
	before := revision()
	state = refresh(true)
	if state["ownPaused"] != true || state["applied"] != true {
		t.Fatalf("exact 0.1 did not pause: %+v", state)
	}
	pausedRevision := revision()
	if pausedRevision != before+1 {
		t.Fatal("first low emitted more than one gate operation")
	}
	money.Store("0.05")
	state = refresh(true)
	money.Store("0.1")
	state = refresh(true)
	if revision() != pausedRevision {
		t.Fatal("consecutive low observations oscillated or emitted duplicate operations")
	}
	// A new precise value above the threshold must restore ONLY this owner.
	money.Store("0.100000000000000000000000000001")
	state = refresh(false)
	if state["ownPaused"] != false {
		t.Fatalf("fresh exact recovery failed: %+v", state)
	}
	if sends.Load() != 4 {
		t.Fatalf("switch/status unexpectedly queried money: %d", sends.Load())
	}
	if revision() != pausedRevision+1 {
		t.Fatal("fresh recovery emitted more than one clear")
	}
	if strings.Contains(fmt.Sprint(state), "MONEY-SYNTHETIC-INFERENCE") {
		t.Fatal("policy leaked secret")
	}
}

func policyMoneyNetwork(t *testing.T, handler http.Handler) balance.NetworkOptions {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(8201), Subject: pkix.Name{CommonName: "controlled official-host fixture"}, DNSNames: []string{"api.deepseek.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return balance.NetworkOptions{RootCAs: roots, LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "127.0.0.1:443" {
			t.Errorf("unpinned fixture dial %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}
}
