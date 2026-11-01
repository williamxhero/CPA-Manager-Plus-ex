package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
)

// A concurrent REAL Core write advances the global CAS after Manager planning.
// The transparent tap forwards every Manager request and Core response unchanged.
func TestCredentialPolicyActualUnrelatedGlobalCASUsesFreshImmutableSuccessor(t *testing.T) {
	runSelectedKeyPolicyHTTP(t, false, false, false, func(f *selectedKeyPolicyFixture) {
		var catalog struct{ Sources []cq.Source }
		f.coreAPI("GET", "credential-quota/sources", nil, &catalog)
		var unrelated cq.Source
		for _, source := range catalog.Sources {
			if source.Protocol == "codex-oauth-wham" {
				unrelated = source
			}
		}
		if unrelated.AuthIndex == "" {
			t.Fatal("actual independent source missing")
		}
		direct := func(ctx context.Context, method, path string, body, out any) error {
			data, _ := json.Marshal(body)
			request, err := http.NewRequestWithContext(ctx, method, f.base+"/v8/management/"+path, bytes.NewReader(data))
			if err != nil {
				return err
			}
			request.Header.Set("Authorization", "Bearer test-password")
			request.Header.Set("Content-Type", "application/json")
			response, err := f.client.Do(request)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			raw, err := io.ReadAll(response.Body)
			if err != nil {
				return err
			}
			if response.StatusCode != 200 {
				return fmt.Errorf("real independent Core status=%d", response.StatusCode)
			}
			return json.Unmarshal(raw, out)
		}
		target, _ := url.Parse(f.base)
		proxy := httputil.NewSingleHostReverseProxy(target)
		var mu sync.Mutex
		var attempts []cp.GateOperation
		var once sync.Once
		var foreign cp.GateOperation
		tap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					http.Error(w, "fixture read failed", 500)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(data))
				var op cp.GateOperation
				if json.Unmarshal(data, &op) != nil {
					t.Error("invalid actual Manager operation")
					http.Error(w, "fixture invalid request", 500)
					return
				}
				if strings.HasPrefix(op.Owner, cp.OwnerNamespace) {
					mu.Lock()
					attempts = append(attempts, op)
					mu.Unlock()
					var injectErr error
					once.Do(func() {
						var ledger cp.GateSnapshot
						injectErr = direct(r.Context(), "GET", "inference-gates", nil, &ledger)
						if injectErr != nil {
							return
						}
						foreign = cp.GateOperation{Version: 1, OpID: uuid.NewString(), ExpectedGateRevision: ledger.GateRevision, Binding: unrelated.Binding, AuthIndex: unrelated.AuthIndex, SourceVersion: unrelated.SourceVersion, Subject: "credential", Owner: "independent-global-cas/" + uuid.NewString(), Action: "pause", Reason: "independent_restriction", EvidenceID: uuid.NewString()}
						var receipt cp.GateResult
						injectErr = direct(r.Context(), "POST", "inference-gates/operations", foreign, &receipt)
						if injectErr != nil {
							return
						}
						var after cp.GateSnapshot
						injectErr = direct(r.Context(), "GET", "inference-gates", nil, &after)
						if injectErr == nil && !after.Confirms(foreign, receipt) {
							injectErr = fmt.Errorf("real independent restriction not confirmed")
						}
					})
					if injectErr != nil {
						t.Error(injectErr)
						http.Error(w, "real independent write failed", 500)
						return
					}
				}
			}
			proxy.ServeHTTP(w, r)
		}))
		t.Cleanup(tap.Close)
		cfg, present, err := f.db.LoadManagerConfig(context.Background())
		if err != nil || !present {
			t.Fatal("saved connection absent")
		}
		cfg.CPAConnection.CPABaseURL = tap.URL
		if err := f.db.SaveManagerConfig(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		f.onPause = func(status cp.Status) {
			mu.Lock()
			posts := append([]cp.GateOperation(nil), attempts...)
			mu.Unlock()
			if len(posts) != 2 || status.Operation == nil || status.Operation.Retry != 1 || status.Operation.PreviousOpID != posts[0].OpID || status.Operation.OpID != posts[1].OpID || posts[0].OpID == posts[1].OpID {
				t.Fatal("typed revision conflict did not produce exactly one fresh-ID successor")
			}
			if posts[1].ExpectedGateRevision != posts[0].ExpectedGateRevision+1 {
				t.Fatal("unexpected real global revision successor")
			}
			next := posts[1]
			next.OpID = posts[0].OpID
			next.ExpectedGateRevision = posts[0].ExpectedGateRevision
			if !reflect.DeepEqual(next, posts[0]) {
				t.Fatal("replan changed original authority/owner/evidence/action body")
			}
		}
		f.onEnd = func() {
			var ledger cp.GateSnapshot
			f.coreAPI("GET", "inference-gates", nil, &ledger)
			if !ledger.Ready || len(ledger.Gates) != 1 || ledger.Gates[0].Owner != foreign.Owner || !reflect.DeepEqual(ledger.Gates[0].Targets, foreign.Targets) || ledger.Gates[0].EvidenceID != foreign.EvidenceID {
				t.Fatal("consumer own clear changed independent global-CAS owner")
			}
		}
	})
}
