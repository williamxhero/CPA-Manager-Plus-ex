package credentialpolicy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Offline/local journal fixture: no positive ON API and no fake successful gate.
func TestCredentialPolicyReconcileRetainsPendingOnUnavailableOrMalformedACK(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"unavailable", "GATE-SECRET-CANARY", 503},
		{"malformed-applied", `{"version":1,"status":"applied"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, writes atomic.Int64
			edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes.Add(1)
					w.WriteHeader(500)
					return
				}
				reads.Add(1)
				w.WriteHeader(tc.code)
				w.Write([]byte(tc.body))
			}))
			defer edge.Close()
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			ctx := context.Background()
			if err := db.SaveManagerConfig(ctx, store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: edge.URL, ManagementKey: "synthetic-key"}}); err != nil {
				t.Fatal(err)
			}
			id, err := db.CredentialPolicyInstallation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := policy.OwnerNamespace + id
			source, now := windowEvidence(t)
			_ = now
			state := policy.Status{Source: source.Source, SubjectID: strings.Repeat("a", 64), Generation: 1, Owner: owner, Enabled: true, State: "pending"}
			// Obtain issued-subject hashing from the public service's SubjectID helper;
			// this local intent is NOT an accepted ON through the Manager endpoint.
			state.SubjectID = policy.SubjectID(source.Source)
			document, _ := json.Marshal(state)
			if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 1, Document: string(document)}, 0); err != nil {
				t.Fatal(err)
			}
			op := policy.GateOperation{Version: 1, OpID: "00000000-0000-4000-8000-000000000009", Binding: source.Binding, AuthIndex: source.AuthIndex, SourceVersion: source.SourceVersion, Subject: "credential", Owner: owner, Action: "pause", Reason: "quota_windows_exhausted", EvidenceID: "00000000-0000-4000-8000-000000000008"}
			pending := policy.PendingIntent{Operation: op, Source: source.Source, Evidence: policy.EvidenceDecision{Action: "pause", Reason: "quota_windows_exhausted", EvidenceVersion: "unit-fixture", ObservedAtMS: time.Now().UnixMilli()}, ExpiresAtMS: time.Now().Add(time.Minute).UnixMilli()}
			body, _ := json.Marshal(pending)
			lease, err := db.AcquireCredentialPolicyLease(ctx, "00000000-0000-4000-8000-000000000007", time.Now().UnixMilli(), 30000)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.BeginCredentialPolicyOperation(ctx, store.CredentialPolicyOperation{ID: op.OpID, SubjectID: state.SubjectID, Generation: 1, Owner: owner, Automatic: true, Body: string(body)}, lease, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if err := db.ReleaseCredentialPolicyLease(ctx, lease); err != nil {
				t.Fatal(err)
			}
			coordinator := policy.New(db, nil, mc.New(cfg, db, nil), false)
			if err := coordinator.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			saved, err := db.CredentialPolicyOperation(ctx, op.OpID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.State != "pending" || saved.Result != "" || strings.Contains(saved.LastError, "CANARY") {
				t.Fatalf("unavailable/malformed ACK became applied: %+v", saved)
			}
			if reads.Load() != 1 || writes.Load() != 0 {
				t.Fatalf("unknown outcome inferred a gate write: read=%d write=%d", reads.Load(), writes.Load())
			}
			if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 2, Document: `{"enabled":false,"owner":"` + owner + `"}`}, 1); err != nil {
				t.Fatal(err)
			}
			before := writes.Load()
			if err := coordinator.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			if writes.Load() != before {
				t.Fatal("OFF retried a late automatic operation")
			}
		})
	}
}
