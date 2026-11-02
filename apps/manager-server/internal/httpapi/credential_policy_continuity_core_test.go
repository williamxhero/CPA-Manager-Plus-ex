package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
)

// Public actual native grant, not an auth-file edit relabelled as refresh. The
// initial fixture enrolls a controlled credential BEFORE policy ON; only Core's
// subsequent ForceRefreshAuth certificate may carry the enabled intent.
func TestCredentialPolicyActualHealthyONNativeRefreshAndReplacementFence(t *testing.T) {
	runActualNativePolicyContinuity(t, false)
}

func TestCredentialPolicyActualPausedNativeRefreshFreshAutomaticRecovery(t *testing.T) {
	runActualNativePolicyContinuity(t, true)
}

func runActualNativePolicyContinuity(t *testing.T, paused bool) {
	f, _, _ := newSelectedKeyPolicyFixture(t, true)
	core := func(path string, body any) {
		raw, _ := json.Marshal(body)
		request, _ := http.NewRequest("POST", f.base+"/v8/management/"+path, bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer test-password")
		request.Header.Set("Content-Type", "application/json")
		response, err := f.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != 200 {
			t.Fatalf("actual isolated Core %s failed status=%d body=%s", path, response.StatusCode, data)
		}
	}
	jwt := "a." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"controlled-issuer","sub":"controlled-subject","https://api.openai.com/auth":{"chatgpt_account_id":"synthetic-continuity-account"}}`)) + ".b"
	enroll := func(account, token string) {
		identityToken := jwt
		if account != "synthetic-continuity-account" {
			claims, _ := json.Marshal(map[string]any{"iss": "controlled-issuer", "sub": "different-controlled-subject", "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account}})
			identityToken = "a." + base64.RawURLEncoding.EncodeToString(claims) + ".b"
		}
		core("credentials?name=codex.json", map[string]any{"type": "codex", "access_token": token, "refresh_token": "SYNTHETIC-INITIAL-GRANT", "account_id": account, "id_token": identityToken, "expired": "2099-01-01T00:00:00Z", "disabled": false})
	}
	currentSource := func() cq.Source {
		var inventory struct{ Sources []cq.Source }
		f.api("GET", "/usage-service/credential-quota", nil, 200, &inventory)
		var selected cq.Source
		for _, source := range inventory.Sources {
			if source.Protocol == "codex-oauth-wham" {
				if selected.AuthIndex != "" {
					t.Fatal("ambiguous native fixture")
				}
				selected = source
			}
		}
		if selected.AuthIndex == "" {
			t.Fatal("native issued source missing")
		}
		return selected
	}
	query := func(source cq.Source) {
		var status cq.Status
		f.api("GET", "/usage-service/credential-quota?authIndex="+source.AuthIndex, nil, 200, &status)
		var job cq.Job
		f.api("POST", "/usage-service/credential-quota", cq.Query{Binding: status.Binding, AuthIndex: status.AuthIndex, SourceID: status.SourceID, SubjectID: status.SubjectID, SourceVersion: status.SourceVersion, Version: status.Version}, 202, &job)
		deadline := time.After(5 * time.Second)
		for {
			select {
			case event := <-f.completed:
				if event.AttemptID != job.ID {
					continue
				}
				if event.Credential == nil || event.Credential.Attempt == nil || event.Credential.Attempt.QueryStatus != "success" {
					if event.Credential != nil && event.Credential.Attempt != nil {
						t.Logf("actual native observation: %+v", *event.Credential.Attempt)
					}
					t.Fatalf("actual native query failed: %+v", event.Credential)
				}
				return
			case <-deadline:
				t.Fatal("actual native completed query missing")
			}
		}
	}
	before := currentSource()
	query(before)
	generation := int64(0)
	enabled := true
	var saved cp.Status
	f.api("PUT", "/usage-service/credential-policy", cp.Update{Binding: before.Binding, AuthIndex: before.AuthIndex, SourceVersion: before.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled}, 200, &saved)
	if !saved.Enabled || saved.Generation != 1 || saved.Operation != nil {
		t.Fatal("healthy ON lacked real query prerequisite")
	}
	wait := func(source cq.Source, action string) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			var status cp.Status
			f.api("GET", "/usage-service/credential-policy?authIndex="+source.AuthIndex, nil, 200, &status)
			if status.Operation != nil && status.Operation.Action == action && status.Operation.CurrentApplied && status.Operation.ReceiptConfirmed {
				return
			}
			if time.Now().After(deadline) {
				for len(f.completed) > 0 {
					event := <-f.completed
					if event.Credential != nil && event.Credential.Attempt != nil {
						t.Logf("DIAG continuity completed job=%s protocol=%s status=%s windows=%s", event.AttemptID, event.Credential.Protocol, event.Credential.Attempt.QueryStatus, event.Credential.Attempt.WindowsJSON)
						inbox, err := f.db.CredentialPolicyEvent(context.Background(), event.AttemptID)
						t.Logf("DIAG continuity inbox state=%s reason=%s read=%v", inbox.State, inbox.Reason, err)
					}
				}
				if status.Operation != nil {
					t.Logf("actual continuity operation: %+v", *status.Operation)
				}
				t.Fatalf("actual native continuity automatic %s missing", action)
			}
		}
	}
	var stopLifecycle func()
	defer func() {
		if stopLifecycle != nil {
			stopLifecycle()
		}
	}()
	startLifecycle := func() {
		if stopLifecycle != nil {
			t.Fatal("duplicate continuity lifecycle")
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- f.manager.AppContext().FullRefreshService.Run(ctx) }()
		stopLifecycle = func() {
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
				t.Error("continuity lifecycle did not join")
			}
		}
	}
	f.sendHTTP()
	if f.oauthSends.Load() != 1 || f.sends[0].Load() != 0 {
		t.Fatal("actual initial native OAuth source not selected")
	}
	if paused {
		f.depleted.Store(true)
		// A recently queried source may correctly be periodic not_due.
		// Explicit public same76 acquisition captures the current ON generation.
		query(before)
		startLifecycle()
		wait(before, "pause")
		f.sendHTTP()
		if f.oauthSends.Load() != 1 || f.sends[0].Load() != 1 {
			t.Fatal("paused native source admitted new business")
		}
		f.depleted.Store(false)
	}
	core("credentials/refresh", map[string]string{"name": "codex.json", "auth_index": before.AuthIndex})
	if f.grants.Load() != 1 {
		t.Fatal("actual ForceRefreshAuth did not reach exactly one controlled native grant")
	}
	after := currentSource()
	if after.Targets[0] == before.Targets[0] {
		t.Fatal("native refresh did not rotate issued full credential authority")
	}
	// No policy/UI read or save may be required to rebind the enabled intent.
	// The real next admitted query must resolve certified continuity itself.
	query(after)
	var rebound cp.Status
	f.api("GET", "/usage-service/credential-policy?authIndex="+after.AuthIndex, nil, 200, &rebound)
	if !rebound.Enabled || rebound.Generation != 2 || rebound.Owner != saved.Owner || rebound.SubjectID == saved.SubjectID {
		t.Fatalf("certified healthy ON continuity lost or invented ACK: %+v", rebound)
	}
	if rebound.Applied && (rebound.Operation == nil || !rebound.Operation.ReceiptConfirmed || !rebound.Operation.CurrentApplied || rebound.Operation.Action != "clear") {
		t.Fatal("continuity migration fabricated applied state instead of a real fresh recovery receipt")
	}
	if paused {
		wait(after, "clear")
		f.sendHTTP()
		if f.oauthSends.Load() != 2 || f.sends[0].Load() != 1 {
			t.Fatal("fresh certified current native recovery did not restore routing")
		}
	} else {
		f.sendHTTP()
		if f.oauthSends.Load() != 2 || f.sends[0].Load() != 0 {
			t.Fatal("healthy certified rotation broke real native business")
		}
		// Healthy ON had NO prior gate row. Only the actual producer certificate
		// may carry policy authority; prove it through NEW automatic admission,
		// not just administrative enabled/readback after the token grant.
		f.depleted.Store(true)
		query(after)
		startLifecycle()
		wait(after, "pause")
		f.sendHTTP()
		if f.oauthSends.Load() != 2 || f.sends[0].Load() != 1 {
			t.Fatal("healthy rotated ON failed to stop actual new native attempts")
		}
		f.depleted.Store(false)
		query(after)
		wait(after, "clear")
		f.sendHTTP()
		if f.oauthSends.Load() != 3 || f.sends[0].Load() != 1 {
			t.Fatal("healthy rotated ON failed fresh automatic own recovery")
		}
	}
	var oldBootQuota cq.Status
	f.api("GET", "/usage-service/credential-quota?authIndex="+after.AuthIndex, nil, 200, &oldBootQuota)
	f.restart()
	var staleReply any
	f.api("POST", "/usage-service/credential-quota", cq.Query{Binding: oldBootQuota.Binding, AuthIndex: oldBootQuota.AuthIndex, SourceID: oldBootQuota.SourceID, SubjectID: oldBootQuota.SubjectID, SourceVersion: oldBootQuota.SourceVersion, Version: oldBootQuota.Version}, 409, &staleReply)
	restarted := currentSource()
	if restarted.Scope.BootEpoch == after.Scope.BootEpoch || restarted.Scope.InstanceID != after.Scope.InstanceID || restarted.Scope.InstanceIncarnation != after.Scope.InstanceIncarnation || !reflect.DeepEqual(restarted.Targets, after.Targets) {
		t.Fatal("restart did not preserve exact lifecycle with a newly issued boot")
	}
	var restartIntent cp.Status
	f.api("GET", "/usage-service/credential-policy?authIndex="+restarted.AuthIndex, nil, 200, &restartIntent)
	if !restartIntent.Enabled || restartIntent.Generation != 2 || restartIntent.SubjectID != rebound.SubjectID || restartIntent.Owner != saved.Owner {
		t.Fatal("restart lost certified intent without UI resave")
	}
	if paused {
		f.depleted.Store(true)
		query(restarted)
		wait(restarted, "pause")
		f.sendHTTP()
		if f.oauthSends.Load() != 2 || f.sends[0].Load() != 2 {
			t.Fatal("new-boot depletion did not stop actual native attempts")
		}
		f.depleted.Store(false)
		query(restarted)
		wait(restarted, "clear")
		f.sendHTTP()
		if f.oauthSends.Load() != 3 || f.sends[0].Load() != 2 {
			t.Fatal("new-boot fresh recovery did not restore actual native route")
		}
	} else {
		query(restarted)
	}
	enroll("different-controlled-account", "DIFFERENT-CONTROLLED-ACCESS")
	// This isolated process fixture has no production disk watcher. A REAL
	// restart republishes the ordinary replacement; it does not certify refresh
	// or invent targets. Old operation scope/boot must remain unusable.
	f.restart()
	replacement := currentSource()
	var replaced cp.Status
	f.api("GET", "/usage-service/credential-policy?authIndex="+replacement.AuthIndex, nil, 200, &replaced)
	if replaced.Enabled || replaced.Generation != 0 || replaced.Applied {
		t.Fatalf("ordinary account replacement inherited ON: %+v", replaced)
	}
}
