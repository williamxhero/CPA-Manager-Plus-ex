package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
)

// First vertical slice: actual native HTTP admission after real #76 depletion.
// Do not replace the public ON write or completion worker with seeded state.
func TestCredentialPolicyNativeAdmissionHTTPDepletion(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	for _, name := range []string{"A", "B"} {
		src := f.sources[name]
		generation := int64(0)
		enabled := name == "A"
		var saved cp.Status
		f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: src.Binding, AuthIndex: src.AuthIndex, SourceVersion: src.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 200, &saved)
		if saved.Enabled != enabled || saved.Generation != 1 {
			t.Fatalf("%s intent not independently saved: %+v", name, saved)
		}
	}
	f.sendHTTP()
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 0 {
		t.Fatal("native priority did not select exact A before pause")
	}
	f.depleted.Store(true)
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
			t.Error("production lifecycle did not join")
		}
	}()
	seen := map[string]bool{}
	attempts := map[string]string{}
	deadline := time.After(15 * time.Second)
	for len(seen) < 2 {
		select {
		case event := <-f.completed:
			if event.Credential == nil {
				continue
			}
			for _, name := range []string{"A", "B"} {
				if event.Credential.AuthIndex == f.sources[name].AuthIndex {
					if event.Credential.Attempt == nil || event.Credential.Attempt.QueryStatus != "success" {
						t.Fatalf("%s actual depletion query failed: %+v", name, event.Credential)
					}
					seen[name] = true
					attempts[name] = event.AttemptID
				}
			}
		case <-deadline:
			t.Fatal("actual #76 native A/B completions missing")
		}
	}
	// A public current-source read is the observation barrier, never a manually
	// driven coordinator Consume/ProcessCompletions/Reconcile call.
	until := time.Now().Add(10 * time.Second)
	for {
		a := f.status("A")
		if a.Operation != nil && a.Operation.Action == "pause" && a.Operation.CurrentApplied && a.Operation.ReceiptConfirmed {
			break
		}
		if time.Now().After(until) {
			// Failure-only diagnostics are not acceptance through a DB back door.
			// They explain why the real public current-applied barrier stayed RED.
			for _, name := range []string{"A", "B"} {
				event, err := f.db.CredentialPolicyEvent(ctx, attempts[name])
				var candidate struct {
					Decision cp.EvidenceDecision `json:"decision"`
				}
				_ = json.Unmarshal([]byte(event.Document), &candidate)
				t.Logf("DIAG ONLY %s completion inbox state=%s reason=%s decision=%+v readError=%v", name, event.State, event.Reason, candidate.Decision, err)
			}
			t.Fatalf("production worker did not apply actual A depletion: enabled=%t bindingCurrent=%t availability=%+v operation=%+v", a.Enabled, a.BindingCurrent, a.Availability, a.Operation)
		}
	}
	b := f.status("B")
	if b.Enabled || b.Operation != nil {
		t.Fatalf("OFF B inherited A automatic action: %+v", b)
	}
	f.sendHTTP()
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 1 {
		t.Fatal("paused A admitted new HTTP or OFF B failed to receive it")
	}
	if f.quotaCalls[0].Load() != 1 || f.quotaCalls[1].Load() != 1 {
		t.Fatal("native quota did not use exactly one selected A/B credential")
	}
}
