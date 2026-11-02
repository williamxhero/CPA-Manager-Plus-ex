package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
)

// The public roster, round request, durable job and real Core query must carry
// the same host-issued authority. Only the external quota provider is replaced.
func TestFullRefreshActualCoreNativeQueryPreservesIssuedSourceAuthority(t *testing.T) {
	f := newRefreshFixture(t)
	code, data := f.api("GET", "/usage-service/credential-quota", nil)
	var view struct{ Sources []cq.Source }
	if code != 200 || json.Unmarshal(data, &view) != nil {
		t.Fatalf("issued sources: HTTP%d %s", code, data)
	}
	issued := map[string]cq.Source{}
	for _, source := range view.Sources {
		if source.State == "ready" {
			if source.SourceID == "" || source.SubjectID == "" {
				t.Fatal("ready source missing host-issued authority")
			}
			issued[source.AuthIndex] = source
		}
	}
	code, data = f.api("GET", "/usage-service/full-refresh", nil)
	var inventory fr.Inventory
	if code != 200 || json.Unmarshal(data, &inventory) != nil || len(issued) != 2 {
		t.Fatalf("real native/plugin inventory missing: HTTP%d %s", code, data)
	}
	var native, other cq.Source
	for _, source := range issued {
		if source.Protocol == "codex-oauth-wham" {
			native = source
		} else {
			other = source
		}
	}
	if native.AuthIndex == "" || other.AuthIndex == "" {
		t.Fatal("independent native/plugin sources required")
	}
	var query cq.Query
	for _, source := range inventory.Sources {
		if source.Request.Credential != nil && source.AuthIndex == native.AuthIndex {
			query = *source.Request.Credential
		}
	}
	if query.SourceID != native.SourceID || query.SubjectID != native.SubjectID || !reflect.DeepEqual(query.Binding, native.Binding) || query.SourceVersion != native.SourceVersion {
		t.Fatal("full-refresh SourceRequest lost issued native authority")
	}
	for _, test := range []struct {
		name   string
		change func(*cq.Query)
	}{
		{"missing_source", func(q *cq.Query) { q.SourceID = "" }},
		{"missing_subject", func(q *cq.Query) { q.SubjectID = "" }},
		{"other_source", func(q *cq.Query) { q.SourceID = other.SourceID }},
		{"other_subject", func(q *cq.Query) { q.SubjectID = other.SubjectID }},
		{"source_version", func(q *cq.Query) { q.SourceVersion += "-stale" }},
		{"target_version", func(q *cq.Query) { q.Targets[0].CredentialVersion++ }},
		{"query_version", func(q *cq.Query) { q.Version++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			q := query
			q.Targets = append(q.Targets[:0:0], q.Targets...)
			test.change(&q)
			code, data := f.api("POST", "/usage-service/credential-quota", q)
			if code != 409 || f.calls.Load() != 0 {
				t.Fatalf("mismatched authority authorized work: HTTP%d %s calls=%d", code, data, f.calls.Load())
			}
		})
	}
	code, data = f.api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "manual"})
	var round fr.Progress
	if code != 202 || json.Unmarshal(data, &round) != nil {
		t.Fatalf("round start: HTTP%d %s", code, data)
	}
	done := f.waitRound(round.ID)
	completed := 0
	for _, item := range done.Items {
		q := item.Request.Credential
		if q == nil || issued[q.AuthIndex].AuthIndex == "" {
			continue
		}
		source := issued[q.AuthIndex]
		if item.Outcome != "success" || q.SourceID != source.SourceID || q.SubjectID != source.SubjectID || item.LastSuccess == nil || item.LastSuccess.QueryID != item.JobID {
			t.Fatalf("native round failed or lost issued authority: %+v", item)
		}
		code, data = f.api("GET", "/usage-service/credential-quota/jobs/"+item.JobID, nil)
		var job cq.Job
		if code != 200 || json.Unmarshal(data, &job) != nil || job.Query.SourceID != source.SourceID || job.Query.SubjectID != source.SubjectID || !reflect.DeepEqual(job.Query.Binding, source.Binding) {
			t.Fatalf("durable query lost authority: HTTP%d %s", code, data)
		}
		completed++
	}
	if completed != 2 || done.Failed != 0 || f.nativeCalls.Load() != 2 || f.calls.Load() != 2 {
		t.Fatalf("selected native/plugin round incomplete: succeeded=%d failed=%d calls=%d nativeCalls=%d", completed, done.Failed, f.calls.Load(), f.nativeCalls.Load())
	}
	t.Logf("issued native/plugin authorities retained through public round and durable jobs: completed=%d upstream_queries=%d", completed, f.calls.Load())
}
