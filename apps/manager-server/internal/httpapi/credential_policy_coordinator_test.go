package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
)

// Trusted76 Start -> realSQLite fence before controlled quota upstream. OFF
// intent/source proof only: this old immutable Core fixture proves NO gate ACK.
func TestCredentialPolicyCoordinatorCapturesKnownOFFBeforeUpstream(t *testing.T) {
	f := newRefreshFixture(t)
	code, data := f.api("GET", "/usage-service/credential-quota", nil)
	if code != 200 {
		t.Fatalf("sources %d", code)
	}
	var sources struct {
		Sources []cq.Source `json:"sources"`
	}
	if json.Unmarshal(data, &sources) != nil {
		t.Fatal("invalid sources")
	}
	var source cq.Source
	for _, candidate := range sources.Sources {
		if candidate.State == "ready" && candidate.Protocol == "codex-oauth-wham" {
			source = candidate
			break
		}
	}
	if source.AuthIndex == "" {
		t.Fatal("no actual native source")
	}
	var update map[string]any
	if err := json.Unmarshal([]byte(policyBody(t, source, 0, false)), &update); err != nil {
		t.Fatal(err)
	}
	code, data = f.api("PUT", "/usage-service/credential-policy", update)
	if code != 200 {
		t.Fatalf("OFF %d %s", code, data)
	}
	var saved cp.Status
	if json.Unmarshal(data, &saved) != nil {
		t.Fatal("invalid OFF")
	}
	started := make(chan fr.StartedObservation, 1)
	gate := make(chan struct{})
	defer close(gate)
	f.manager.AppContext().FullRefreshService.SubscribeStarted(func(ctx context.Context, event fr.StartedObservation) error {
		if event.Source.Kind != fr.Credential {
			return nil
		}
		fence, err := f.db.CredentialPolicyQuery(ctx, event.AttemptID)
		if err != nil {
			return err
		}
		if fence.SubjectID != saved.SubjectID || fence.Owner != saved.Owner || fence.Generation != 1 || fence.Enabled {
			t.Error("missing/stale trusted policy fence before upstream")
		}
		started <- event
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	code, data = f.api("GET", "/usage-service/credential-quota?authIndex="+source.AuthIndex, nil)
	if code != 200 {
		t.Fatal("query status unavailable")
	}
	var status cq.Status
	if json.Unmarshal(data, &status) != nil {
		t.Fatal("invalid query status")
	}
	query := cq.Query{Binding: source.Binding, SourceVersion: source.SourceVersion, AuthIndex: source.AuthIndex, Version: status.Version}
	code, data = f.api("POST", "/usage-service/credential-quota", query)
	if code != 202 {
		t.Fatalf("same76 start %d %s", code, data)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("policy fence not present before trusted started observer")
	}
	if f.nativeCalls.Load() != 0 {
		t.Fatal("upstream ran before committed policy generation fence")
	}
}
