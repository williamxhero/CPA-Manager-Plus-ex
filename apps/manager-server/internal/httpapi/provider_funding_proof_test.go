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
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const fundingProofPath = "/usage-service/provider-funding-proof"

func TestProviderFundingProofCurrentSourcesSurviveSQLiteReopen(t *testing.T) {
	f := newProviderFundingFixture(t)
	before := f.ledger()
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingProofNetwork(t))
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
	state := f.api("GET", f.policyPath(), nil, 200)
	request := map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": f.providerID}
	for _, phase := range []string{"before_reopen", "after_reopen"} {
		t.Run(phase, func(t *testing.T) {
			parent := f.t
			f.t = t
			defer func() { f.t = parent }()
			if phase == "after_reopen" {
				f.reopen()
			}
			proof := f.api("POST", fundingProofPath, request, 200)
			if proof["ready"] != true || proof["authority"] != "runtime_key_matched_official_balances" {
				t.Fatalf("complete current proof was not accepted: %+v", proof)
			}
			sources, ok := proof["sources"].([]any)
			if !ok || len(sources) != 2 {
				t.Fatalf("expected two independently verified member sources: %+v", proof)
			}
			seen := map[string]bool{}
			for _, raw := range sources {
				source := raw.(map[string]any)
				members := source["memberIds"].([]any)
				amounts := source["balances"].([]any)
				if source["keyMatched"] != true || source["fresh"] != true || source["version"] != float64(1) || len(members) != 1 || len(amounts) != 1 {
					t.Fatalf("invalid source authority: %+v", source)
				}
				id := members[0].(string)
				if seen[id] || amounts[0].(map[string]any)["currency"] != "USD" || amounts[0].(map[string]any)["total"] != "0.0500" {
					t.Fatalf("duplicate member or coerced amount: %+v", source)
				}
				seen[id] = true
			}
			for _, member := range f.members {
				if !seen[member.Targets[0].ResourceID] {
					t.Fatal("proof omitted a current routed member")
				}
			}
		})
	}
	if f.operations.Load() != 0 || !reflect.DeepEqual(before, f.ledger()) {
		t.Fatal("read-only funding proof changed actual Core gates")
	}
	state = f.api("GET", f.policyPath(), nil, 200)
	if state["enabled"] != false || state["effective"] != false || state["applied"] != false || state["generation"] != float64(0) {
		t.Fatal("proof was confused with policy intent or applied gate")
	}
}

func (f *providerFundingFixture) configureAndRefresh(memberKey, queryKey string, version int64) map[string]any {
	f.t.Helper()
	b := f.members[memberKey]
	f.api("PUT", balancePath, ob.Update{Config: ob.Config{Binding: b, Version: version, Adapter: "deepseek", Endpoint: "https://api.deepseek.com/user/balance", PrivateTargets: []string{"127.0.0.1"}}, Secret: queryKey}, 200)
	job := f.api("POST", balancePath, ob.Query{Binding: b, Version: version + 1}, 202)
	for deadline := time.Now().Add(10 * time.Second); ; {
		current := f.api("GET", balancePath+"?job="+job["id"].(string), nil, 200)
		if current["state"] == "complete" {
			status, ok := current["status"].(map[string]any)
			if !ok || status["lastSuccess"] == nil || status["lastSuccess"].(map[string]any)["queryStatus"] != "success" {
				f.t.Fatalf("controlled official read did not actually succeed: %+v", current)
			}
			return status
		}
		if current["state"] != "running" || time.Now().After(deadline) {
			f.t.Fatalf("official observation did not complete: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *providerFundingFixture) proofRequest() map[string]any {
	f.t.Helper()
	state := f.api("GET", f.policyPath(), nil, 200)
	return map[string]any{"scope": state["scope"], "targets": state["targets"], "providerId": f.providerID}
}

func (f *providerFundingFixture) currentSources() {
	f.t.Helper()
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingProofNetwork(f.t))
	for _, key := range []string{"FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2"} {
		f.configureAndRefresh(key, key, 0)
	}
}

func (f *providerFundingFixture) unchanged(before map[string]any) {
	f.t.Helper()
	if f.operations.Load() != 0 || !reflect.DeepEqual(before, f.ledger()) {
		f.t.Fatal("funding verification changed actual Core gates")
	}
}

func TestProviderFundingProofStaleCannotGateGroup(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	before := f.ledger()
	request := f.proofRequest()
	valid := f.api("POST", fundingProofPath, request, 200)
	latest := int64(0)
	for _, raw := range valid["sources"].([]any) {
		latest = max(latest, int64(raw.(map[string]any)["observedAtMs"].(float64)))
	}
	// Time is the external boundary; no sleeping or direct SQLite edits.
	f.manager.AppContext().ProviderPolicyService.Clock = func() time.Time { return time.UnixMilli(latest + 3600000) }
	f.api("POST", fundingProofPath, request, 503)
	f.api("POST", fundingProofPath, request, 503)
	f.unchanged(before)
	f.manager.AppContext().ProviderPolicyService.Clock = time.Now
	f.api("POST", fundingProofPath, request, 200)
}

func TestProviderFundingProofWrongKeyWithSuccessfulBalanceCannotGateGroup(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	before := f.ledger()
	request := f.proofRequest()
	f.api("POST", fundingProofPath, request, 200)
	rejected := f.keyRejected.Load()
	// A successful balance response using the sibling key is not A1 funding.
	f.configureAndRefresh("FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A2", 1)
	f.api("POST", fundingProofPath, request, 503)
	if f.keyRejected.Load() != rejected+1 {
		t.Fatal("wrong query key was not actually rejected by Core key-match")
	}
	f.unchanged(before)
}

func TestProviderFundingProofSourceVersionNeedsNewSuccess(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	before := f.ledger()
	request := f.proofRequest()
	f.api("POST", fundingProofPath, request, 200)
	b := f.members["FUNDING-SYNTHETIC-A1"]
	f.api("PUT", balancePath, ob.Update{Config: ob.Config{Binding: b, Version: 1, Adapter: "deepseek", Endpoint: "https://api.deepseek.com/user/balance", PrivateTargets: []string{"127.0.0.1"}}, Secret: "FUNDING-SYNTHETIC-A1"}, 200)
	f.api("POST", fundingProofPath, request, 503)
	f.unchanged(before)
}

func TestProviderFundingProofMissingMemberCannotGateGroup(t *testing.T) {
	f := newProviderFundingFixture(t)
	before := f.ledger()
	f.manager.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(providerFundingProofNetwork(t))
	f.configureAndRefresh("FUNDING-SYNTHETIC-A1", "FUNDING-SYNTHETIC-A1", 0)
	f.api("POST", fundingProofPath, f.proofRequest(), 503)
	f.unchanged(before)
}

func TestProviderFundingProofRejectsRouteAndChallengeMismatch(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	before := f.ledger()
	request := f.proofRequest()
	f.api("POST", fundingProofPath, request, 200)
	for i, name := range []string{"partial_route_coverage", "wrong_nonce", "wrong_source_version", "wrong_authority"} {
		t.Run(name, func(t *testing.T) {
			parent := f.t
			f.t = t
			defer func() { f.t = parent }()
			f.proofFault.Store(int32(i + 1))
			matches := f.keyMatches.Load()
			f.api("POST", fundingProofPath, request, 503)
			if i == 0 && f.keyMatches.Load() != matches {
				t.Fatal("partial routing proof bypassed qualification")
			}
			f.unchanged(before)
		})
	}
	f.proofFault.Store(0)
	f.api("POST", fundingProofPath, request, 200)
}

func TestProviderFundingProofRejectsSourceChangeDuringChallenge(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	before := f.ledger()
	request := f.proofRequest()
	f.api("POST", fundingProofPath, request, 200)
	b := f.members["FUNDING-SYNTHETIC-A1"]
	raw, err := json.Marshal(ob.Update{Config: ob.Config{Binding: b, Version: 1, Adapter: "deepseek", Endpoint: "https://api.deepseek.com/user/balance", PrivateTargets: []string{"127.0.0.1"}}, Secret: "FUNDING-SYNTHETIC-A1"})
	if err != nil {
		t.Fatal(err)
	}
	updated := make(chan int, 1)
	var once sync.Once
	f.beforeMatch = func() {
		once.Do(func() {
			// This runs on the proxy goroutine: return the outcome to the
			// test goroutine without invoking testing.Fatal/FailNow here.
			r := httptest.NewRequest("PUT", balancePath, strings.NewReader(string(raw)))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
			w := httptest.NewRecorder()
			f.manager.Handler().ServeHTTP(w, r)
			updated <- w.Code
		})
	}
	f.api("POST", fundingProofPath, request, 503)
	select {
	case status := <-updated:
		if status != 200 {
			t.Fatalf("source change fixture failed: status=%d", status)
		}
	default:
		t.Fatal("source change was never injected")
	}
	f.unchanged(before)
}

func TestProviderFundingProofPublicAuthenticationAndBinding(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	before := f.ledger()
	request := f.proofRequest()
	raw, _ := json.Marshal(request)
	testutil.RequireStatus(t, testutil.Request(t, f.manager.Handler(), "POST", fundingProofPath, string(raw), ""), 401)
	testutil.RequireStatus(t, testutil.Request(t, f.manager.Handler(), "GET", fundingProofPath, "", testutil.AdminKey), 405)
	request["funding"] = true
	f.api("POST", fundingProofPath, request, 400)
	delete(request, "funding")
	targets := request["targets"].([]any)
	targets[1].(map[string]any)["credential_version"] = float64(999)
	f.api("POST", fundingProofPath, request, 409)
	f.unchanged(before)
}

func TestProviderFundingProofRejectsConnectionChangeAfterChallenge(t *testing.T) {
	f := newProviderFundingFixture(t)
	f.currentSources()
	before := f.ledger()
	request := f.proofRequest()
	f.api("POST", fundingProofPath, request, 200)
	config := f.api("GET", "/usage-service/config", nil, 200)["config"].(map[string]any)
	// Keep this isolated fixture's monitoring stopped; enabling it would write
	// Core usage configuration and change scope for an unrelated reason.
	config["collector"].(map[string]any)["enabled"] = false
	connection := config["cpaConnection"].(map[string]any)
	target, err := url.Parse(connection["cpaBaseUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	// A second saved connection reaches the SAME Core through the original
	// management boundary. Scope and all issued targets deliberately stay equal.
	second := httptest.NewServer(httputil.NewSingleHostReverseProxy(target))
	t.Cleanup(second.Close)
	calls := 0
	f.manager.AppContext().ProviderPolicyService.Clock = func() time.Time {
		calls++
		// The first two calls validate the member observations; the third
		// timestamps the result AFTER the complete Core key challenge returns.
		if calls == 3 {
			connection["cpaBaseUrl"] = second.URL
			f.api("PUT", "/usage-service/config", map[string]any{"config": config}, 200)
		}
		return time.Now()
	}
	f.api("POST", fundingProofPath, request, 409)
	if calls < 3 {
		t.Fatal("connection change did not occur after the key challenge")
	}
	f.manager.AppContext().ProviderPolicyService.Clock = time.Now
	if f.api("GET", "/usage-service/config", nil, 200)["config"].(map[string]any)["cpaConnection"].(map[string]any)["cpaBaseUrl"] != second.URL {
		t.Fatal("public configuration change was not saved")
	}
	// A fresh request on the new connection can still prove the same evidence.
	f.api("POST", fundingProofPath, request, 200)
	f.unchanged(before)
}

// Only external DNS/socket/TLS roots are controlled; real HTTPS validation and
// the ordinary official balance parser and encrypted persistence still execute.
func providerFundingProofNetwork(t *testing.T) balance.NetworkOptions {
	return providerFundingNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"0.0500","granted_balance":"0","topped_up_balance":"0.0500"}]}`)
	})
}

func providerFundingNetwork(t *testing.T, respond http.HandlerFunc) balance.NetworkOptions {
	network, _, _ := providerFundingTLS(t, respond)
	return network
}

func providerFundingTLS(t *testing.T, respond http.HandlerFunc) (balance.NetworkOptions, []byte, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(8203), Subject: pkix.Name{CommonName: "controlled proof fixture"}, DNSNames: []string{"api.deepseek.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.deepseek.com" || r.Method != "GET" || r.URL.Path != "/user/balance" {
			t.Error("unexpected controlled official request")
			w.WriteHeader(400)
			return
		}
		respond(w, r)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return balance.NetworkOptions{RootCAs: roots, LookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "api.deepseek.com" {
			return nil, fmt.Errorf("unexpected fixture hostname")
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "127.0.0.1:443" {
			return nil, fmt.Errorf("unexpected fixture address")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}, der, server.Listener.Addr().String()
}
