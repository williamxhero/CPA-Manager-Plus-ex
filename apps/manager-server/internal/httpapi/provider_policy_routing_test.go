package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	pp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerpolicy"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

// This exercises PUBLIC Manager official-query/policy APIs and the actual
// ordinary Core's inference API. It never substitutes a gate ACK, a routing
// executor, the official origin, or the key-match proof. The external deny proxy
// returns 502 to CONNECT without dialing, resolving, tunneling, or completing
// inference. A post-recovery CONNECT proves admission, NOT a completed answer.
func TestProviderPolicyActualCoreRoutingPauseAndFreshRecovery(t *testing.T) {
	f := newPolicyUnsentFixture(t)
	original := f.status()
	if !original.Enabled || original.OwnPaused || original.ManualDisabled || !original.BindingCurrent || original.Locked {
		t.Fatalf("requires an enabled, actually issued, manually enabled clean provider: %+v", original)
	}
	revisionBefore := f.revision()

	// Only the owned loopback Core is an HTTP client target. Do not inherit a
	// machine proxy or follow redirects to an uncontrolled host.
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("routing fixture redirects refused")
	}}
	request := func(method, path string, payload []byte) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, f.core.base+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer MONEY-CLIENT")
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatalf("owned loopback Core %s %s did not return an HTTP response: %v", method, path, err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"MONEY-SYNTHETIC-INFERENCE", "MONEY-CORE-MANAGEMENT"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("public Core inference response leaked controlled credentials")
			}
		}
		return response.StatusCode, raw
	}

	// The fixture has exactly one issued parent/member. Obtain its advertised
	// model from Core rather than guessing a model/provider from a display label.
	code, raw := request(http.MethodGet, "/v1/models", nil)
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &models) != nil || len(models.Data) != 1 || models.Data[0].ID == "" {
		t.Fatalf("actual one-provider fixture did not advertise exactly one model: status=%d body=%s", code, raw)
	}
	payload, err := json.Marshal(map[string]any{
		"model":    models.Data[0].ID,
		"messages": []map[string]string{{"role": "user", "content": "controlled admission check; external proxy must deny"}},
		"stream":   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkIdentity := func(state pp.Status) {
		t.Helper()
		if !state.Enabled || !state.BindingCurrent || state.Locked || state.Generation != original.Generation || state.SubjectID != original.SubjectID || state.Owner != original.Owner || state.ManualDisabled != original.ManualDisabled || !reflect.DeepEqual(state.Binding, original.Binding) {
			t.Fatalf("source query/inference changed issued identity, ON intent, owner, generation, or manual disabled: %+v", state)
		}
	}
	type inferenceError struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}

	// There is intentionally no pre-pause failed inference: that optional
	// sample could introduce an independent transport/credential cooldown.
	// The identical post-clear request below is the counter's positive control.
	low := f.query("0.05")
	f.awaitApplied("routing-low-pause", low.ID, true, revisionBefore+1, original)
	paused := f.status()
	checkIdentity(paused)
	if paused.State != "paused" || !paused.Applied || !paused.OwnPaused || f.revision() != revisionBefore+1 {
		t.Fatalf("actual successful low query did not apply exactly one own monetary pause: %+v", paused)
	}
	allBeforePause := f.core.denyConnects.Load()
	originBeforePause := f.core.denyDeepSeekConnects.Load()
	for i := 0; i < 2; i++ {
		code, raw = request(http.MethodPost, "/v1/chat/completions", payload)
		var denied inferenceError
		if err := json.Unmarshal(raw, &denied); err != nil {
			t.Errorf("paused inference %d lacks a structured public error: status=%d body=%s", i+1, code, raw)
		} else if code != http.StatusServiceUnavailable || denied.Error.Type != "server_error" || denied.Error.Code != "internal_server_error" || denied.Error.Message != "inference admission unavailable or restricted" {
			// Core's typed InferenceAdmissionError exposes 503 and this exact
			// message through the standard 5xx envelope. The cooldown marker
			// inference_gate_denied is internal, not a public HTTP error code.
			// Require the full contract, never an arbitrary 503/network error.
			t.Errorf("paused inference %d requires the typed admission HTTP 503 contract (server_error/internal_server_error and exact admission message): status=%d body=%s", i+1, code, raw)
		}
		if got := f.core.denyDeepSeekConnects.Load(); got != originBeforePause {
			t.Errorf("paused inference %d reached the controlled inference-origin CONNECT boundary: before=%d after=%d", i+1, originBeforePause, got)
		}
	}
	allAfterPause := f.core.denyConnects.Load()
	stillPaused := f.status()
	checkIdentity(stillPaused)
	if !stillPaused.Applied || !stillPaused.OwnPaused || stillPaused.State != "paused" || f.revision() != revisionBefore+1 {
		t.Fatalf("denied inference changed the saved monetary restriction: %+v", stillPaused)
	}
	t.Logf("paused completed requests: api.deepseek.com:443 CONNECT=%d->%d; ALL origins CONNECT=%d->%d (background hosts excluded, no global zero-egress claim)", originBeforePause, f.core.denyDeepSeekConnects.Load(), allBeforePause, allAfterPause)

	// This new successful observation is strictly >0.1, not a cached balance,
	// an administrative clear, an OFF write, or an alteration of manual state.
	high := f.query("0.100000000000000000000000000001")
	if high.ID == low.ID || f.queries.Load() != 2 {
		t.Fatal("recovery must use a distinct actual successful official query")
	}
	f.awaitApplied("routing-fresh-clear", high.ID, false, revisionBefore+2, original)
	recovered := f.status()
	checkIdentity(recovered)
	if recovered.State != "observing" || !recovered.Applied || recovered.OwnPaused || f.revision() != revisionBefore+2 {
		t.Fatalf("fresh high query did not clear this monetary owner exactly once: %+v", recovered)
	}
	_, forwarded, _ := f.wire.snapshot()
	accepted := f.assertExactReceipts(revisionBefore, "pause", "clear")
	if len(forwarded) != 2 || len(accepted) != 2 || accepted[0] == accepted[1] {
		t.Fatalf("pause/clear require two distinct real Core durable/applied receipts: forwarded=%d accepted=%v", len(forwarded), accepted)
	}
	for i, wantAction := range []string{"pause", "clear"} {
		var operation struct {
			Scope      rc.Scope    `json:"scope"`
			Targets    []rc.Target `json:"targets"`
			OpID       string      `json:"opId"`
			Owner      string      `json:"owner"`
			Action     string      `json:"action"`
			EvidenceID string      `json:"evidenceId"`
		}
		if err := json.Unmarshal(forwarded[i], &operation); err != nil || operation.OpID != accepted[i] || operation.Owner != original.Owner || operation.Action != wantAction || operation.Scope != original.Scope || !reflect.DeepEqual(operation.Targets, original.Targets) || !strings.HasPrefix(operation.EvidenceID, "balance/") {
			t.Fatalf("actual %s command did not preserve this issued group/monetary owner and real balance evidence: %+v err=%v", wantAction, operation, err)
		}
	}

	originBeforeRecoverySend := f.core.denyDeepSeekConnects.Load()
	code, raw = request(http.MethodPost, "/v1/chat/completions", payload) // EXACT same issued model/body as while paused.
	var networkFailure inferenceError
	if err := json.Unmarshal(raw, &networkFailure); err != nil {
		t.Fatalf("readmitted inference did not return a structured controlled network failure: status=%d body=%s", code, raw)
	}
	networkFailureVerified := code >= 500 && code <= 599 && networkFailure.Error.Code != "inference_gate_denied" && strings.Contains(networkFailure.Error.Message, http.StatusText(http.StatusBadGateway))
	if !networkFailureVerified {
		t.Errorf("readmitted request must fail at the deny CONNECT proxy, not complete inference or remain gated: status=%d body=%s", code, raw)
	}
	originAfterRecoverySend := f.core.denyDeepSeekConnects.Load()
	if originAfterRecoverySend <= originBeforeRecoverySend {
		t.Errorf("fresh monetary recovery did not readmit the identical request to the controlled api.deepseek.com:443 CONNECT boundary: before=%d after=%d", originBeforeRecoverySend, originAfterRecoverySend)
	}
	final := f.status()
	checkIdentity(final)
	if final.OwnPaused || !final.Applied || final.State != "observing" || f.revision() != revisionBefore+2 {
		t.Fatalf("controlled inference network failure changed monetary intent/owner: %+v", final)
	}
	t.Logf("actual ordinary Core revision=%s sha256=%s; low=%s freshHigh=%s model=%s; post-clear inference-origin CONNECT=%d->%d status=%d admissionOnlyVerified=%t; deny proxy never forwards, no real completion claimed; process logs=%s", f.core.commit, f.core.digest, low.ID, high.ID, models.Data[0].ID, originBeforeRecoverySend, originAfterRecoverySend, code, networkFailureVerified && originAfterRecoverySend > originBeforeRecoverySend, f.core.logs)
}
