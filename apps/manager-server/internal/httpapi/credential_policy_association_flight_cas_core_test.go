package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	a "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy/association"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

// A genuine positive Core proof is held AFTER Core evaluated it, while Manager
// performs a full literal profile/secret CAS. The old completed attempt cannot
// plan a gate. A later same76 query may act and recover, proving stale work does
// not poison the independent next inbox/journal entry. No private Consume call.
func TestCredentialPolicyActualAssociationFlightCASRejectsStaleProofAllowsFreshWork(t *testing.T) {
	for _, mode := range []string{"profile-CAS", "secret-CAS"} {
		t.Run(mode, func(t *testing.T) {
			runSelectedKeyPolicyHTTP(t, false, false, false, func(f *selectedKeyPolicyFixture) {
				var hold atomic.Bool
				var writes atomic.Int32
				proofReady := make(chan a.Result, 1)
				release := make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				target, _ := url.Parse(f.base)
				proxy := httputil.NewSingleHostReverseProxy(target)
				proxy.ModifyResponse = func(response *http.Response) error {
					if response.Request.URL.Path != "/v8/management/credential-quota/credential-association" || response.StatusCode != 200 {
						return nil
					}
					data, err := io.ReadAll(response.Body)
					if err != nil {
						return err
					}
					response.Body = io.NopCloser(bytes.NewReader(data))
					result, err := a.DecodeResult(data)
					if err != nil {
						t.Error(err)
						return nil
					}
					if result.Associated && hold.CompareAndSwap(true, false) {
						proofReady <- result
						select {
						case <-release:
						case <-response.Request.Context().Done():
							return response.Request.Context().Err()
						}
					}
					return nil
				}
				tap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
						writes.Add(1)
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
				f.onPrerequisite = func(configured ob.Status, selected, other cq.Source) {
					defer unblock()
					generation, on := int64(0), true
					var state cp.Status
					f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: selected.Binding, AuthIndex: selected.AuthIndex, SourceVersion: selected.SourceVersion, ExpectedGeneration: &generation, Enabled: &on}, 200, &state)
					if !state.Enabled || state.Generation != 1 || state.Applied {
						t.Fatal("real prerequisite failed to save original ON")
					}
					f.sendHTTP()
					query := func() ob.Job {
						var job ob.Job
						f.api("POST", balancePath, ob.Query{Binding: configured.Binding, Version: configured.Version}, 202, &job)
						deadline := time.After(5 * time.Second)
						for {
							select {
							case event := <-f.completed:
								if event.AttemptID != job.ID {
									continue
								}
								if event.Official == nil || event.Official.Attempt == nil || event.Official.Attempt.QueryStatus != "success" {
									t.Fatal("independent real billing failed")
								}
								return job
							case <-deadline:
								t.Fatal("same76 completed query missing")
							}
						}
					}
					f.depleted.Store(true)
					hold.Store(true)
					stale := query()
					select {
					case proof := <-proofReady:
						if proof.QueryID != stale.ID || proof.AuthIndex != selected.AuthIndex {
							t.Fatal("held proof was not the original selected completed attempt")
						}
					case <-time.After(10 * time.Second):
						t.Fatal("actual positive association flight not reached")
					}
					changed := configured.Config
					update := ob.Update{Config: changed}
					// Reaffirming the same literal profile still advances its CAS.
					// Unlike changing any74 authority field, it preserves secretRef,
					// isolating the version-CAS guard from secret-CAS below.
					sourceID := cp.SubjectID(cq.Source{Binding: cq.Binding{Scope: configured.Scope, Targets: configured.Targets}})
					beforeCAS, err := f.db.OfficialBalance(context.Background(), sourceID)
					if err != nil {
						t.Fatal(err)
					}
					if mode != "profile-CAS" {
						update.Secret = "SELECTED-KEY-A" /* new secretRef/CAS, same actual key */
					}
					f.api("PUT", balancePath, update, 200, &configured)
					afterCAS, err := f.db.OfficialBalance(context.Background(), sourceID)
					if err != nil || (mode == "profile-CAS") != (beforeCAS.SecretRef == afterCAS.SecretRef) {
						t.Fatal("test did not isolate profile versus secret CAS", err)
					}
					if configured.Version != 2 {
						t.Fatal("literal CAS did not advance")
					}
					unblock()
					fresh := query()
					if fresh.ID == stale.ID {
						t.Fatal("new current profile reused completed old job")
					}
					wait := func(action, attempt string) {
						deadline := time.Now().Add(10 * time.Second)
						for {
							state = f.status("A")
							if state.Operation != nil && state.Operation.Action == action && state.Operation.CurrentApplied && state.Operation.ReceiptConfirmed {
								if !strings.Contains(state.Operation.EvidenceVersion, ":"+attempt+":") {
									t.Fatal("stale association authorized current applied gate")
								}
								return
							}
							if time.Now().After(deadline) {
								t.Fatalf("fresh current-CAS %s did not progress: %+v", action, state.Operation)
							}
						}
					}
					wait("pause", fresh.ID)
					if writes.Load() != 1 {
						t.Fatal("stale proof wrote a gate before fresh completed evidence")
					}
					f.sendHTTP()
					f.depleted.Store(false)
					recovery := query()
					wait("clear", recovery.ID)
					f.sendHTTP()
					if writes.Load() != 2 || f.quotaCalls[0].Load() != 4 || f.sends[0].Load() != 2 || f.sends[1].Load() != 1 {
						t.Fatal("CAS race duplicated/stalled acquisition or real gate/business actions")
					}
					var otherState cp.Status
					f.api("GET", "/usage-service/credential-policy?authIndex="+other.AuthIndex, nil, 200, &otherState)
					if otherState.Enabled || otherState.Generation != 0 || otherState.Operation != nil {
						t.Fatal("CAS recovery widened to other credential")
					}
				}
			})
		})
	}
}
