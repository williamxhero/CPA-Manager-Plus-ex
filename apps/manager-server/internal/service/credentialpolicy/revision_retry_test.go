package credentialpolicy_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
)

// Pure journal planning model only; NOT a Core rejection/ACK/admission proof.
func TestCredentialPolicyRevisionRetryIsBoundedAndPreservesEvidence(t *testing.T) {
	status, now := windowEvidence(t)
	decision := policy.ValidateCredentialEvidence(status, now)
	intent := policy.PendingIntent{Planned: true, Source: status.Source, Evidence: decision, ExpiresAtMS: now.Add(time.Minute).UnixMilli(), Operation: policy.GateOperation{Version: 1, OpID: uuid.NewString(), ExpectedGateRevision: 7, Binding: status.Binding, SourceVersion: status.SourceVersion, AuthIndex: status.AuthIndex, Owner: policy.OwnerNamespace + uuid.NewString(), Subject: "credential", Action: "pause", Reason: "quota_windows_exhausted", EvidenceID: uuid.NewString()}}
	ledger := policy.GateSnapshot{Version: 1, Scope: status.Scope, Ready: true, GateRevision: 8, Gates: []policy.GateRestriction{}}
	next, reason := policy.PrepareGateRevisionRetry(intent, status.Source, ledger, now)
	if reason != "" || next.Operation.OpID == intent.Operation.OpID || next.Retry != 1 || next.PreviousOpID != intent.Operation.OpID || next.Operation.ExpectedGateRevision != 8 || !reflect.DeepEqual(next.Source, intent.Source) || !reflect.DeepEqual(next.Evidence, intent.Evidence) {
		t.Fatalf("invalid immutable successor: %+v %s", next, reason)
	}
	before, _ := json.Marshal(intent)
	for _, name := range []string{"exhausted", "expired", "same-revision", "corrupt-ledger", "changed-binding", "own-row-changed"} {
		t.Run(name, func(t *testing.T) {
			candidate := intent
			current := status.Source
			snapshot := ledger
			switch name {
			case "exhausted":
				candidate.Retry = 3
			case "expired":
				candidate.ExpiresAtMS = now.UnixMilli()
			case "same-revision":
				snapshot.GateRevision = 7
			case "corrupt-ledger":
				snapshot.Ready = false
			case "changed-binding":
				current.Scope.BootEpoch = uuid.NewString()
			case "own-row-changed":
				snapshot.Gates = []policy.GateRestriction{{Owner: intent.Operation.Owner, Targets: intent.Source.Targets, Model: "", Applicability: "current"}}
			}
			if _, reason := policy.PrepareGateRevisionRetry(candidate, current, snapshot, now); reason == "" {
				t.Fatal("unsafe fresh-ID successor planned")
			}
		})
	}
	after, _ := json.Marshal(intent)
	if string(before) != string(after) {
		t.Fatal("sent predecessor mutated")
	}
}
