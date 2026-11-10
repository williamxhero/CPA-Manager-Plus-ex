package credentialpolicy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

type autoStopSources []cq.Source

func (sources autoStopSources) Sources(context.Context) ([]cq.Source, error) { return sources, nil }

func autoStopTestSource() cq.Source {
	return cq.Source{Binding: cq.Binding{Scope: rc.Scope{InstanceID: "00000000-0000-4000-8000-000000000001", InstanceIncarnation: "00000000-0000-4000-8000-000000000002", BootEpoch: "00000000-0000-4000-8000-000000000003", Revision: 2}, Targets: []rc.Target{{ResourceID: "00000000-0000-4000-8000-000000000004", Incarnation: "00000000-0000-4000-8000-000000000005", IdentityVersion: 1, CredentialVersion: 1, ConfigVersion: 1}}}, SourceID: "issued-source", SubjectID: "issued-subject", SourceVersion: strings.Repeat("a", 64), AuthIndex: "runtime-index", Protocol: "codex-oauth-wham", State: "ready"}
}

// This exercises intent synchronization and restart scope rebinding only.
// Admission is separately tested against the real Core transport.
func TestQuotaAutoStopSyncRestoresSavedIntentAndPropagatesOFF(t *testing.T) {
	ctx := context.Background()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	installation, err := db.CredentialPolicyInstallation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := autoStopTestSource()
	state := Status{Source: source, SubjectID: SubjectID(source), Enabled: true, Generation: 4, Owner: OwnerNamespace + installation}
	doc, err := marshalPolicyDocument(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 4, Document: string(doc)}, 0); err != nil {
		t.Fatal(err)
	}
	var got []quotaAutoStop
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v8/management/inference-gates/quota-auto-stop" || r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("unexpected quota acquisition or unauthenticated sync")
			w.WriteHeader(500)
			return
		}
		var p quotaAutoStop
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			t.Error("invalid policy sync")
			return
		}
		got = append(got, p)
		json.NewEncoder(w).Encode(p)
	}))
	defer server.Close()
	if err := db.SaveManagerConfig(ctx, store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: server.URL, ManagementKey: "synthetic-key"}}); err != nil {
		t.Fatal(err)
	}
	current := source
	current.Scope.BootEpoch = "00000000-0000-4000-8000-000000000006"
	svc := New(db, autoStopSources{current}, mc.New(cfg, db, nil))
	if err := svc.SyncQuotaAutoStops(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Enabled || got[0].Generation != 4 || !reflect.DeepEqual(got[0].Binding, current.Binding) {
		t.Fatalf("saved ON was not restored against current scope: %+v", got)
	}
	state.Enabled = false
	state.Generation = 5
	doc, err = marshalPolicyDocument(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: state.SubjectID, Generation: 5, Document: string(doc)}, 4); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncQuotaAutoStops(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Enabled || got[1].Generation != 5 {
		t.Fatal("new OFF was not propagated")
	}
}

func TestUpstreamQuotaPauseDisplayRequiresCurrentLedgerAndDurableReceipt(t *testing.T) {
	for _, durable := range []bool{true, false} {
		t.Run(map[bool]string{true: "durable", false: "missing-durability"}[durable], func(t *testing.T) {
			ctx := context.Background()
			cfg := testutil.NewConfig(t)
			db := testutil.NewStore(t, cfg)
			source := autoStopTestSource()
			status := Status{Source: source, Owner: "owner-test", Generation: 1}
			id := "00000000-0000-4000-8000-000000000009"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "operations/"+id) {
					json.NewEncoder(w).Encode(GateResult{Version: 1, OpID: id, Status: "applied", GateRevision: 2, DurableRevision: 2, AppliedRevision: 2, Durable: durable, Applied: true})
					return
				}
				json.NewEncoder(w).Encode(GateSnapshot{Version: 1, Scope: source.Scope, Ready: true, GateRevision: 2, Gates: []GateRestriction{{Applicability: "current", Targets: source.Targets, Owner: status.Owner, Reason: "quota_windows_exhausted", SourceVersion: source.SourceVersion, GateRevision: 2, EvidenceID: "upstream-quota:" + id}}})
			}))
			defer server.Close()
			if err := db.SaveManagerConfig(ctx, store.ManagerConfig{CPAConnection: store.ManagerCPAConnectionConfig{CPABaseURL: server.URL, ManagementKey: "synthetic-key"}}); err != nil {
				t.Fatal(err)
			}
			svc := New(db, nil, mc.New(cfg, db, nil))
			svc.attachUpstreamQuotaPause(ctx, &status)
			if status.Applied != durable || durable && (status.Operation == nil || !status.Operation.CurrentApplied || status.Operation.Action != "pause") {
				t.Fatalf("unverified pause display: %+v", status)
			}
		})
	}
}
