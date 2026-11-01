package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

// The real Core commits before its ACK is deliberately lost. A profile CAS then
// invalidates the original association. Receipt-only reconciliation must still
// recover the SAME immutable operation; stale evidence cannot authorize a resend
// or a new plan. A NEW actual qualifying query then recovers the own pause.
func TestCredentialPolicyActualOfficialLostACKThenProfileCASReconcilesSameOperation(t *testing.T) {
	runActualOfficialLostACK(t, false)
}

func TestCredentialPolicyActualOfficialLostACKManagerRestartReconcilesSameOperation(t *testing.T) {
	runActualOfficialLostACK(t, true)
}

func runActualOfficialLostACK(t *testing.T, restart bool) {
	runSelectedKeyPolicyHTTP(t, false, false, false, func(f *selectedKeyPolicyFixture) {
		var mu sync.Mutex
		var posts []cp.GateOperation
		var once sync.Once
		committed := make(chan cp.GateOperation, 1)
		target, _ := url.Parse(f.base)
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ModifyResponse = func(response *http.Response) error {
			if response.Request.Method != "POST" || response.Request.URL.Path != "/v8/management/inference-gates/operations" || response.StatusCode != 200 {
				return nil
			}
			mu.Lock()
			op := posts[len(posts)-1]
			mu.Unlock()
			if op.Action != "pause" {
				return nil
			}
			var lost bool
			once.Do(func() {
				data, err := io.ReadAll(response.Body)
				if err != nil {
					t.Error(err)
					return
				}
				response.Body = io.NopCloser(bytes.NewReader(data))
				var receipt cp.GateResult
				if json.Unmarshal(data, &receipt) != nil || !receipt.Confirms(op) {
					t.Error("actual Core committed receipt missing")
					return
				}
				committed <- op
				lost = true
			})
			if lost {
				return errors.New("controlled lost actual committed ACK")
			}
			return nil
		}
		tap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/v8/management/inference-gates/operations" {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					http.Error(w, "fixture read", 500)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(data))
				var op cp.GateOperation
				if json.Unmarshal(data, &op) != nil {
					t.Error("invalid actual operation")
					http.Error(w, "fixture decode", 500)
					return
				}
				mu.Lock()
				posts = append(posts, op)
				mu.Unlock()
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
		var original cp.GateOperation
		var immutable string
		f.beforePauseWait = func(configured *ob.Status) {
			select {
			case original = <-committed:
			case <-time.After(10 * time.Second):
				t.Fatal("actual automatic committed pause not observed")
			}
			record, err := f.db.CredentialPolicyOperation(context.Background(), original.OpID)
			if err != nil {
				t.Fatal(err)
			}
			immutable = record.Body // secondary immutable-journal assertion, not ACK oracle
			if restart {
				f.cycleManager() // stopped/joined runtime, actual SQLite close/reopen
				return
			}
			changed := configured.Config
			protocol := *changed.NewAPI
			protocol.InstanceVersion = "synthetic-after-real-commit-v2"
			changed.NewAPI = &protocol
			//74 correctly clears credentials when the literal source contract
			// changes. Reauthorize explicitly, never carry a secret implicitly.
			f.api("PUT", balancePath, ob.Update{Config: changed, Secret: "SELECTED-KEY-A"}, 200, configured)
			if configured.Version != 2 {
				t.Fatal("profile CAS did not invalidate original association")
			}
		}
		f.onPause = func(status cp.Status) {
			if status.Operation == nil || status.Operation.OpID != original.OpID || !status.Operation.ReceiptConfirmed || !status.Operation.CurrentApplied {
				t.Fatal("lost ACK was not reconciled from actual unchanged receipt/current gate")
			}
			record, err := f.db.CredentialPolicyOperation(context.Background(), original.OpID)
			if err != nil || record.Body != immutable {
				t.Fatal("profile CAS rewrote sent immutable operation", err)
			}
			mu.Lock()
			n := len(posts)
			mu.Unlock()
			if n != 1 {
				t.Fatal("stale association resent/replanned a committed pause")
			}
		}
		f.onEnd = func() {
			mu.Lock()
			defer mu.Unlock()
			if len(posts) != 2 || posts[0].OpID != original.OpID || posts[1].Action != "clear" || posts[1].OpID == original.OpID {
				t.Fatal("fresh profile did not produce only one separate real own recovery")
			}
		}
	})
}
