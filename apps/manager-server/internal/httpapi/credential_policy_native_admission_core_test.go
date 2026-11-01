package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
)

func TestCredentialPolicyNativeAdmissionRecoveryPreservesManualAndOtherOwner(t *testing.T) {
	f := newNativeAdmissionFixture(t, true)
	if !f.sources["A"].ManualDisabled {
		t.Fatal("actual native A not manually disabled")
	}
	f.enableAB()
	f.pauseThrough76()
	paused := f.status("A")
	var before cp.GateSnapshot
	f.coreAPI("GET", "inference-gates", nil, &before)
	// A separate owner's restriction is an authenticated REAL Core management
	// precondition, not quota evidence, a forged ACK, or our automatic action.
	op := cp.GateOperation{Version: 1, OpID: uuid.NewString(), ExpectedGateRevision: before.GateRevision, Binding: paused.Binding, AuthIndex: paused.AuthIndex, SourceVersion: paused.SourceVersion, Subject: "credential", Owner: "native-admission-independent-owner", Action: "pause", Reason: "independent_restriction", EvidenceID: uuid.NewString()}
	var receipt cp.GateResult
	f.coreAPI("POST", "inference-gates/operations", op, &receipt)
	var withOther cp.GateSnapshot
	f.coreAPI("GET", "inference-gates", nil, &withOther)
	if !withOther.Confirms(op, receipt) {
		t.Fatal("independent owner precondition not actually applied")
	}
	var foreign cp.GateRestriction
	for _, gate := range withOther.Gates {
		if gate.Owner == op.Owner {
			foreign = gate
		}
	}
	recovered := f.recoverThrough76()
	if !recovered.ManualDisabled {
		t.Fatal("automatic recovery overrode manual disable")
	}
	var after cp.GateSnapshot
	f.coreAPI("GET", "inference-gates", nil, &after)
	if !after.Ready || len(after.Gates) != 1 || !reflect.DeepEqual(after.Gates[0], foreign) {
		t.Fatal("automatic own clear changed independent restriction or retained own pause")
	}
	f.sendHTTP()
	if f.sends[0].Load() != 0 || f.sends[1].Load() != 1 {
		t.Fatal("manual/other-owner restricted A received business HTTP after own recovery")
	}
}

func TestCredentialPolicyNativeAdmissionFreshReplenishmentAutomaticallyRecovers(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	f.enableAB()
	f.pauseThrough76()
	f.sendHTTP()
	if f.sends[0].Load() != 0 || f.sends[1].Load() != 1 {
		t.Fatal("depleted A admitted HTTP before recovery")
	}
	f.recoverThrough76()
	f.sendHTTP()
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 1 {
		t.Fatal("fresh same-credential automatic clear did not restore A")
	}
}

func (f *nativeAdmissionFixture) recoverThrough76() cp.Status {
	t := f.t
	t.Helper()
	before := f.status("A")
	if before.Operation == nil || before.Operation.Action != "pause" || !before.Operation.CurrentApplied || !before.Operation.ReceiptConfirmed {
		t.Fatal("missing real automatic A pause precondition")
	}
	baseline := [2]int64{f.quotaCalls[0].Load(), f.quotaCalls[1].Load()}
	f.depleted.Store(false)
	var current cq.Status
	f.api("GET", "/usage-service/credential-quota?authIndex="+f.sources["A"].AuthIndex, nil, 200, &current)
	if current.SourceVersion != f.sources["A"].SourceVersion || current.Targets[0] != f.sources["A"].Targets[0] {
		t.Fatal("recovery changed native credential authority")
	}
	var job cq.Job
	f.api("POST", "/usage-service/credential-quota", cq.Query{Binding: current.Binding, SourceVersion: current.SourceVersion, AuthIndex: current.AuthIndex, Version: current.Version}, 202, &job)
	deadline := time.After(15 * time.Second)
	for {
		select {
		case event := <-f.completed:
			if event.AttemptID != job.ID {
				continue
			}
			if event.Credential == nil || event.Credential.AuthIndex != current.AuthIndex || event.Credential.Attempt == nil || event.Credential.Attempt.QueryStatus != "success" || event.Credential.Stale {
				t.Fatal("actual same76 replenishment completion failed")
			}
			goto completed
		case <-deadline:
			t.Fatal("actual76 replenishment completion missing")
		}
	}
completed:
	until := time.Now().Add(10 * time.Second)
	for {
		a := f.status("A")
		if a.Operation != nil && a.Operation.Action == "clear" && a.Operation.OpID != before.Operation.OpID && a.Operation.CurrentApplied && a.Operation.ReceiptConfirmed {
			if a.Owner != before.Owner || a.Generation != before.Generation || !a.Enabled || a.SourceVersion != before.SourceVersion {
				t.Fatal("automatic recovery changed owner/intent/authority")
			}
			if f.quotaCalls[0].Load()-baseline[0] != 1 || f.quotaCalls[1].Load()-baseline[1] != 0 {
				t.Fatal("replenishment did not query exactly selected same A")
			}
			return a
		}
		if time.Now().After(until) {
			t.Fatalf("fresh same76 query did not automatically clear own pause: operation=%+v", a.Operation)
		}
	}
}

func TestCredentialPolicyNativeAdmissionReusedDownstreamWS(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	f.enableAB()
	turn := f.reusedWSTurn()
	turn()
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 0 {
		t.Fatal("initial WS turn did not select exact A")
	}
	f.pauseThrough76()
	turn()
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 1 {
		t.Fatal("reused WS turn bypassed A pause or failed to admit OFF B")
	}
}

func TestCredentialPolicyNativeAdmissionSSEDrainsAdmittedResponse(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	f.enableAB()
	// Upstream receipt is the admission barrier: the real selector/executor has
	// already admitted A. Do not depend on downstream header-buffering policy.
	type streamResult struct {
		body        []byte
		status      int
		err         error
		contentType string
	}
	drained := make(chan streamResult, 1)
	requestBody := []byte(`{"model":"native-admission-model","input":[{"role":"user","content":"native-admission-drain"}],"stream":true}`)
	streamContext, cancelStream := context.WithCancel(context.Background())
	t.Cleanup(cancelStream)
	go func() {
		transport := &http.Transport{Proxy: nil}
		defer transport.CloseIdleConnections()
		req, _ := http.NewRequestWithContext(streamContext, "POST", f.base+"/v1/responses", bytes.NewReader(requestBody))
		req.Header.Set("Authorization", "Bearer synthetic-client")
		req.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Transport: transport}).Do(req)
		if err != nil {
			drained <- streamResult{err: err}
			return
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		drained <- streamResult{body: data, status: response.StatusCode, err: err, contentType: response.Header.Get("Content-Type")}
	}()
	select {
	case <-f.drainEntered:
	case result := <-drained:
		t.Fatalf("stream ended before admission barrier: %+v", result)
	case <-time.After(10 * time.Second):
		t.Fatal("actual upstream admission missing")
	}
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 0 {
		t.Fatal("admitted stream not attributed to exact A")
	}
	f.pauseThrough76()
	newStream := f.openSSE(false)
	data, err := io.ReadAll(newStream.Body)
	newStream.Body.Close()
	if err != nil || !strings.Contains(string(data), "response.completed") {
		t.Fatalf("new OFF B SSE failed: %s %v", data, err)
	}
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 1 {
		t.Fatal("paused A received new SSE, or OFF B did not receive it")
	}
	f.releaseDrain()
	select {
	case result := <-drained:
		if result.err != nil || result.status != 200 || !strings.Contains(result.contentType, "text/event-stream") || !strings.Contains(string(result.body), "admitted-before-pause") || !strings.Contains(string(result.body), "drained-after-pause") || !strings.Contains(string(result.body), "response.completed") {
			t.Fatalf("admitted A SSE did not drain intact: %+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("admitted A stream did not drain after release")
	}
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 1 {
		t.Fatal("drain replayed an upstream business request")
	}
}

// Actual HTTP admission is observed only after real76 depletion and ACK/readback.
func TestCredentialPolicyNativeAdmissionHTTPDepletion(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	f.enableAB()
	f.sendHTTP()
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 0 {
		t.Fatal("initial HTTP did not select exact A")
	}
	f.pauseThrough76()
	f.sendHTTP()
	if f.sends[0].Load() != 1 || f.sends[1].Load() != 1 {
		t.Fatal("paused A admitted HTTP or OFF B failed")
	}
}

func (f *nativeAdmissionFixture) enableAB() {
	t := f.t
	// Qualify A through the real public76 acquisition while intent is default
	// OFF, before either ON write or policy worker startup. Never seed an ON or
	// observation, recapture an old completion, or call a private Consume path.
	var before cq.Status
	f.api("GET", "/usage-service/credential-quota?authIndex="+f.sources["A"].AuthIndex, nil, 200, &before)
	var prerequisite cq.Job
	f.api("POST", "/usage-service/credential-quota", cq.Query{Binding: before.Binding, AuthIndex: before.AuthIndex, SourceVersion: before.SourceVersion, Version: before.Version}, 202, &prerequisite)
	deadline := time.After(15 * time.Second)
	qualified := false
	for !qualified {
		select {
		case event := <-f.completed:
			if event.AttemptID != prerequisite.ID {
				continue
			}
			if event.Credential == nil || event.Credential.AuthIndex != before.AuthIndex || event.Credential.Attempt == nil || event.Credential.Attempt.QueryID != prerequisite.ID || event.Credential.Attempt.QueryStatus != "success" {
				t.Fatal("actual preON shared76 query failed")
			}
			qualified = true
		case <-deadline:
			t.Fatal("actual preON shared76 completion missing")
		}
	}
	if f.quotaCalls[0].Load() != 1 || f.quotaCalls[1].Load() != 0 {
		t.Fatal("preON acquisition was not exactly selected A")
	}
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
}

func (f *nativeAdmissionFixture) pauseThrough76() {
	t := f.t
	quotaBaseline := [2]int64{f.quotaCalls[0].Load(), f.quotaCalls[1].Load()}
	t.Logf("pre-depletion quota baseline[A,B]=%v (includes real preON prerequisite)", quotaBaseline)
	f.depleted.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.manager.AppContext().FullRefreshService.Run(ctx) }()
	t.Cleanup(func() {
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
	})
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
	if f.quotaCalls[0].Load()-quotaBaseline[0] != 1 || f.quotaCalls[1].Load()-quotaBaseline[1] != 1 {
		t.Fatal("depletion phase did not query each exact selected A/B credential once")
	}
}
