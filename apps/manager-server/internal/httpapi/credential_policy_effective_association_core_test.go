package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	a "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy/association"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

// Real official billing and real business sends remain independent of a failed
// association. The tap observes ACTUAL Core responses and forwards unchanged
// bytes; it never manufactures a negative/positive proof, gate ACK or selector.
func TestCredentialPolicyActualEffectiveAssociationNegativesPreserveBilling(t *testing.T) {
	for _, mode := range []string{"auth-override", "tenant-prefix", "origin", "wallet-PAT"} {
		t.Run(mode, func(t *testing.T) {
			opts := selectedKeyFixtureOptions{}
			if mode == "auth-override" {
				opts.authOverride = "Bearer SELECTED-KEY-B"
			}
			if mode == "tenant-prefix" {
				opts.tenantPrefix = "/tenant/B"
			}
			f, group, endpoint := newSelectedKeyPolicyFixtureOptions(t, opts)
			if mode == "origin" {
				endpoint = f.otherOrigin()
			}
			proofs := make(chan a.Result, 8)
			var gateWrites, associationCalls atomic.Int32
			target, _ := url.Parse(f.base)
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.ModifyResponse = func(response *http.Response) error {
				if response.Request.URL.Path != "/v8/management/credential-quota/credential-association" {
					return nil
				}
				associationCalls.Add(1)
				data, err := io.ReadAll(response.Body)
				if err != nil {
					return err
				}
				response.Body = io.NopCloser(bytes.NewReader(data))
				result, err := a.DecodeResult(data)
				if response.StatusCode != 200 || err != nil {
					t.Error("actual association response invalid")
					return nil
				}
				proofs <- result
				return nil
			}
			tap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
					gateWrites.Add(1)
				}
				proxy.ServeHTTP(w, r)
			}))
			t.Cleanup(tap.Close)
			cfg, _, err := f.db.LoadManagerConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			cfg.CPAConnection.CPABaseURL = tap.URL
			if err := f.db.SaveManagerConfig(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
			update := ob.Update{Config: ob.Config{Binding: group, Adapter: balance.NewAPITokenUsageV1, Endpoint: endpoint + "/api/usage/token/", PrivateTargets: []string{"127.0.0.1"}, NewAPI: &balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "controlled-negative-v1", NativeUnit: "newapi_quota", Auth: balance.NewAPIInferenceKeyReadOnlyV1}}, Secret: "SELECTED-KEY-A"}
			semantic := "key_remaining"
			if mode == "wallet-PAT" {
				update.Adapter, update.Endpoint, update.Secret = balance.NewAPIUserSelfV1, endpoint+"/api/user/self", "WALLET-PAT"
				update.NewAPI.Auth, update.NewAPI.UserID, update.NewAPI.AccountScope = balance.NewAPIUserPATWithIDV1, "42", "explicit-controlled-wallet"
				semantic = "account_wallet"
			}
			var configured ob.Status
			f.api("PUT", balancePath, update, 200, &configured)
			var job ob.Job
			f.api("POST", balancePath, ob.Query{Binding: configured.Binding, Version: configured.Version}, 202, &job)
			select {
			case event := <-f.completed:
				if event.AttemptID != job.ID || event.Official == nil || event.Official.Attempt == nil || event.Official.Attempt.QueryStatus != "success" || event.Official.Attempt.Semantic != semantic {
					t.Fatal("negative association broke independent real billing")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("same76 normal billing completion missing")
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- f.manager.AppContext().FullRefreshService.Run(ctx) }()
			defer func() {
				cancel()
				shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if err := f.manager.AppContext().FullRefreshService.Shutdown(shutdown); err != nil {
					t.Error(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-shutdown.Done():
					t.Error("negative lifecycle did not join")
				}
			}()
			if mode != "wallet-PAT" {
				seen := map[string]bool{}
				deadline := time.After(10 * time.Second)
				for len(seen) < 2 {
					select {
					case result := <-proofs:
						if result.QueryID != job.ID || result.Associated || result.Proof != nil {
							t.Fatal("unproved effective executor was associated")
						}
						want := "association_unsupported"
						if mode == "origin" {
							want = "origin_mismatch"
						}
						if result.Reason != want {
							t.Fatalf("actual precise Core reason=%s want=%s", result.Reason, want)
						}
						seen[result.AuthIndex] = true
					case <-deadline:
						t.Fatal("actual completed inbox never obtained both scoped negative proofs")
					}
				}
			}
			for _, src := range f.sources {
				gen, on := int64(0), true
				var denied struct {
					Error string `json:"error"`
				}
				f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: src.Binding, AuthIndex: src.AuthIndex, SourceVersion: src.SourceVersion, ExpectedGeneration: &gen, Enabled: &on}, 503, &denied)
				if denied.Error != "verified_selected_key_evidence_required" {
					t.Fatalf("wrong precise unavailable reason: %s", denied.Error)
				}
				var state cp.Status
				f.api("GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, nil, 200, &state)
				if state.Enabled || state.Generation != 0 || state.Applied || state.Operation != nil {
					t.Fatal("unsupported association fabricated ON/applied/default coverage")
				}
			}
			f.sendHTTP()
			wantA, wantB := int64(1), int64(0)
			if mode == "auth-override" {
				wantA, wantB = 0, 1
			}
			if f.sends[0].Load() != wantA || f.sends[1].Load() != wantB || f.quotaCalls[0].Load() != 1 || gateWrites.Load() != 0 {
				t.Fatal("negative proof changed real business/auth or duplicated billing/gate writes")
			}
			if mode == "tenant-prefix" && f.tenantBusiness.Load() != 1 {
				t.Fatal("actual executor did not use different deployment prefix")
			}
			if mode == "wallet-PAT" && associationCalls.Load() != 0 {
				t.Fatal("wallet PAT incorrectly entered inference-key proof protocol")
			}
		})
	}
}
