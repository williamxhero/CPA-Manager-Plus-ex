package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	cp "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestCredentialPolicyActualQuerySubjectRefreshPreservesPolicyIdentity(t *testing.T) {
	f := newNativeAdmissionFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.manager.Handler().ServeHTTP(w, r)
	}))
	defer server.Close()
	api := func(method, path string, body any, want int, out any) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
		req.Header.Set("Content-Type", "application/json")
		response, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s %s returned %d want %d", method, path, response.StatusCode, want)
		}
		if out != nil {
			if err := json.NewDecoder(response.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Decode the additive field independently so the original source compiles
	// and fails on the missing public contract, not on a missing Go member.
	var policy struct {
		cp.Status
		QuerySubjectID string `json:"querySubjectId"`
	}
	api("GET", "/usage-service/credential-policy?authIndex="+f.sources["A"].AuthIndex, nil, 200, &policy)
	var quota cq.Status
	api("GET", "/usage-service/credential-quota?authIndex="+policy.AuthIndex, nil, 200, &quota)
	if policy.SubjectID == quota.SubjectID || quota.SubjectID == "" {
		t.Fatal("fixture must issue distinct durable policy and quota-query subjects")
	}
	t.Logf("actual issued subjects policy=%s quota=%s publicQuery=%s", policy.SubjectID, quota.SubjectID, policy.QuerySubjectID)
	if policy.QuerySubjectID != quota.SubjectID {
		t.Fatal("policy read does not expose the authoritative quota-query subject")
	}
	var inventory fr.Inventory
	api("GET", "/usage-service/full-refresh", nil, 200, &inventory)
	var selected []fr.SourceRequest
	for _, source := range inventory.Sources {
		query := source.Request.Credential
		if source.Request.Kind == fr.Credential && query != nil && query.AuthIndex == policy.AuthIndex &&
			query.SourceID == policy.SourceID && query.SubjectID == policy.QuerySubjectID && query.Version == policy.QueryVersion &&
			query.SourceVersion == policy.SourceVersion && source.SourceID == policy.SourceID && source.SubjectID == policy.QuerySubjectID &&
			source.QueryVersion == policy.QueryVersion && reflect.DeepEqual(query.Binding, policy.Binding) &&
			reflect.DeepEqual(source.Targets, policy.Targets) && reflect.DeepEqual(inventory.Scope, policy.Scope) && source.QueryCapable {
			selected = append(selected, source.Request)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("policy query binding matched %d current sources, want one", len(selected))
	}
	wrong := *selected[0].Credential
	wrong.SubjectID = policy.SubjectID
	api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "manual", "source": fr.SourceRequest{Kind: fr.Credential, Credential: &wrong}}, 400, nil)
	if f.quotaCalls[0].Load() != 0 || f.quotaCalls[1].Load() != 0 {
		t.Fatal("durable policy handle authorized a quota query")
	}
	var job fr.Progress
	api("POST", "/usage-service/full-refresh", map[string]any{"purpose": "manual", "source": selected[0]}, 202, &job)
	deadline := time.Now().Add(15 * time.Second)
	for job.State == "running" {
		if time.Now().After(deadline) {
			t.Fatal("selected shared refresh did not complete")
		}
		time.Sleep(10 * time.Millisecond)
		api("GET", "/usage-service/full-refresh?id="+job.ID, nil, 200, &job)
	}
	if job.Succeeded != 1 || job.Failed != 0 || f.quotaCalls[0].Load() != 1 || f.quotaCalls[1].Load() != 0 {
		t.Fatalf("selected refresh did not query exactly A: %+v", job)
	}
	api("GET", "/usage-service/credential-quota?authIndex="+policy.AuthIndex, nil, 200, &quota)
	if quota.Stale || quota.LastSuccess == nil || quota.LastSuccess.QueryStatus != "success" {
		t.Fatal("shared refresh did not persist a public quota success")
	}
	generation, enabled := policy.Generation, true
	api("PUT", "/usage-service/credential-policy", cp.Update{Binding: policy.Binding, AuthIndex: policy.AuthIndex, SourceVersion: policy.SourceVersion, ExpectedGeneration: &generation, Enabled: &enabled, Owner: policy.Owner}, 200, nil)
	// Existing real reopen helper preserves SQLite and joins old workers; it
	// does not seed policy, quota, gate receipts or invent issued authority.
	reopenAuthorityTestManager(f)
	for _, query := range []string{"subjectId=" + policy.SubjectID, "authIndex=" + policy.AuthIndex} {
		var read struct {
			cp.Status
			QuerySubjectID string `json:"querySubjectId"`
		}
		api("GET", "/usage-service/credential-policy?"+query, nil, 200, &read)
		if read.SubjectID != policy.SubjectID || read.QuerySubjectID != quota.SubjectID || read.Owner != policy.Owner || !read.Enabled || read.Generation != generation+1 {
			t.Fatal("mutation/reopen changed durable policy identity or lost issued query identity")
		}
	}
	if f.status("B").Enabled || f.quotaCalls[1].Load() != 0 {
		t.Fatal("selected A refresh changed or queried B")
	}
}
