package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

// Actual public API/SQLite/76 inbox/Core proof and native HTTP. This is NOT the
// OAuth sidecar's route slice; every selected key starts native-query unsupported.
func TestCredentialPolicyActualOfficialSelectedKeyAutomaticHTTP(t *testing.T) {
	runSelectedKeyPolicyHTTP(t, false)
}

func TestCredentialPolicyActualOfficialSelectedKeyUnknownThenUnlimitedRecovery(t *testing.T) {
	runSelectedKeyPolicyHTTP(t, true)
}

func runSelectedKeyPolicyHTTP(t *testing.T, unknownThenUnlimited bool) {
	f, group, endpoint := newSelectedKeyPolicyFixture(t)
	update := ob.Update{Config: ob.Config{Binding: group, Adapter: balance.NewAPITokenUsageV1, Endpoint: endpoint + "/api/usage/token/", PrivateTargets: []string{"127.0.0.1"}, NewAPI: &balance.NewAPIConfig{ProtocolConfirmed: true, InstanceVersion: "synthetic-selected-key-v1", NativeUnit: "newapi_quota", Auth: balance.NewAPIInferenceKeyReadOnlyV1}}, Secret: "SELECTED-KEY-A"}
	var configured ob.Status
	f.api("PUT", balancePath, update, 200, &configured)
	group = configured.Binding // use the public host's complete canonical contract
	var currentAttempt string
	acquire := func() {
		var job ob.Job
		f.api("POST", balancePath, ob.Query{Binding: group, Version: configured.Version}, 202, &job)
		currentAttempt = job.ID
		deadline := time.After(5 * time.Second)
		for {
			select {
			case event := <-f.completed:
				if event.AttemptID != job.ID {
					continue
				}
				if event.Official == nil || event.Official.Attempt == nil || event.Official.Attempt.QueryStatus != "success" || event.Official.Attempt.Semantic != "key_remaining" {
					t.Fatalf("independent official read failed: %+v", event.Official)
				}
				return
			case <-deadline:
				t.Fatal("same76 official completion missing")
			}
		}
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
			t.Error("lifecycle did not join")
		}
	}()
	//76 performs its real startup round. Do not race it with a redundant manual
	// acquisition and then weaken exact-count assertions to hide the duplicate.
	initialDeadline := time.After(5 * time.Second)
	initialComplete := false
	for !initialComplete {
		select {
		case event := <-f.completed:
			if event.Official == nil {
				continue
			}
			if event.Official.Attempt == nil || event.Official.Attempt.QueryStatus != "success" || event.Official.Attempt.Semantic != "key_remaining" {
				t.Fatal("startup official query failed")
			}
			currentAttempt = event.AttemptID
			initialComplete = true
		case <-initialDeadline:
			t.Fatal("76 startup qualifying official query missing")
		}
	}
	// Both selected descriptors were issued by Core; only actual proof may assign
	// the successful query's key evidence to A. B must remain unavailable.
	var selected, other cq.Source
	deadline := time.Now().Add(10 * time.Second)
	for selected.AuthIndex == "" {
		for _, src := range f.sources {
			var status cp.Status
			f.api("GET", "/usage-service/credential-policy?authIndex="+src.AuthIndex, nil, 200, &status)
			if status.QueryAvailability.Reason == "verified_selected_key_evidence" {
				selected = src
			} else {
				other = src
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual association inbox never qualified a selected unsupported source")
		}
	}
	if other.AuthIndex == "" || selected.AuthIndex == other.AuthIndex {
		t.Fatal("proof widened to unrelated configured key")
	}
	f.sources = map[string]cq.Source{"A": selected, "B": other}
	generation := int64(0)
	enabled := true
	var saved cp.Status
	var rejected struct {
		Error string `json:"error"`
	}
	f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: other.Binding, AuthIndex: other.AuthIndex, SourceVersion: other.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 503, &rejected)
	if rejected.Error != "verified_selected_key_evidence_required" {
		t.Fatalf("A proof qualified B or wrong rejection: %+v", rejected)
	}
	f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: selected.Binding, AuthIndex: selected.AuthIndex, SourceVersion: selected.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 200, &saved)
	if !saved.Enabled || saved.Generation != 1 || saved.Applied {
		t.Fatal("ON prerequisite did not persist truthfully")
	}
	enabled = false
	f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: other.Binding, AuthIndex: other.AuthIndex, SourceVersion: other.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 200, &saved)
	f.sendHTTP()
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 0 {
		t.Fatal("actual configured priority did not choose A")
	}
	f.depleted.Store(true)
	acquire()
	wait := func(action string) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			status := f.status("A")
			if status.Operation != nil && status.Operation.Action == action && status.Operation.CurrentApplied && status.Operation.ReceiptConfirmed {
				return
			}
			if time.Now().After(deadline) {
				event, err := f.db.CredentialPolicyEvent(ctx, currentAttempt)
				t.Logf("DIAG ONLY inbox state=%s reason=%s error=%v", event.State, event.Reason, err)
				ops, err := f.db.PendingCredentialPolicyOperations(ctx)
				for _, op := range ops {
					t.Logf("DIAG ONLY journal op=%s state=%s reason=%s attempted=%t", op.ID, op.State, op.LastError, op.Attempted)
				}
				if err != nil {
					t.Logf("DIAG journal read: %v", err)
				}
				if status.Operation != nil {
					t.Logf("DIAG operation: %+v", *status.Operation)
				}
				t.Fatalf("actual selected-key automatic %s missing: %+v", action, status)
			}
		}
	}
	wait("pause")
	f.sendHTTP()
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 1 {
		t.Fatal("authoritatively depleted A admitted a new native attempt")
	}
	if unknownThenUnlimited {
		f.nativeQuotaState.Store(1)
		acquire()
		deadline := time.Now().Add(10 * time.Second)
		for {
			state := f.status("A")
			if state.Availability.Reason == "native_key_value_unknown" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("associated unknown key value not reported unavailable")
			}
		}
		f.sendHTTP()
		if f.sends[0].Load() != 1 || f.sends[1].Load() != 2 {
			t.Fatal("unknown key counter recovered or widened pause")
		}
		f.nativeQuotaState.Store(2)
	}
	f.depleted.Store(false)
	acquire()
	wait("clear")
	f.sendHTTP()
	wantB := int64(1)
	wantQueries := int64(3)
	if unknownThenUnlimited {
		wantB = 2
		wantQueries = 4
	}
	if f.sends[0].Load() != 2 || f.sends[1].Load() != wantB {
		t.Fatal("fresh associated key evidence did not recover exact own pause")
	}
	if f.quotaCalls[0].Load() != wantQueries {
		t.Fatal("duplicate/private official acquisition loop")
	}
	if state := f.status("B"); state.Enabled || state.Operation != nil {
		t.Fatal("unassociated B acquired A policy/gate")
	}
}
