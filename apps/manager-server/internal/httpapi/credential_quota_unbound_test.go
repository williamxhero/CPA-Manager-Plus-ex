package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// A current inventory that publishes no runtime binding for the requested
// credential is not evidence that the instance or credential source changed.
// Reporting identity_changed there tells the operator to refresh a page that
// will keep failing, and hides the real cause (an unbound or blocked source).
func TestCredentialQuotaReadReportsUnboundCredentialWithoutIdentityClaim(t *testing.T) {
	server, paths := sourceViewContractServer(t, sourceViewContractCase{})
	rr := testutil.Request(t, server.Handler(), "GET", "/usage-service/credential-quota?authIndex=fedcba9876543210", "", testutil.AdminKey)
	var rejected struct {
		Error string `json:"error"`
	}
	if rr.Code != 409 || json.Unmarshal(rr.Body.Bytes(), &rejected) != nil || rejected.Error != "current_credential_source_unavailable" {
		t.Fatalf("unbound credential read = HTTP%d %s", rr.Code, rr.Body.String())
	}
	for _, path := range paths() {
		if path == "/v8/management/credential-quota/query" {
			t.Fatal("unbound credential read started a provider query")
		}
	}
}

// A supplied binding that no longer matches is still an identity change.
func TestCredentialQuotaStartStillReportsIdentityChange(t *testing.T) {
	server, _ := sourceViewContractServer(t, sourceViewContractCase{})
	body, err := json.Marshal(map[string]any{
		"scope":         json.RawMessage(`{` + testScopeJSON + `}`),
		"targets":       []any{json.RawMessage(`{` + testTargetJSON + `}`)},
		"auth_index":    "fedcba9876543210",
		"sourceId":      "source-view-id",
		"subjectId":     "subject-view-id",
		"sourceVersion": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"version":       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := testutil.Request(t, server.Handler(), "POST", "/usage-service/credential-quota", string(body), testutil.AdminKey)
	var rejected struct {
		Error string `json:"error"`
	}
	if rr.Code != 409 || json.Unmarshal(rr.Body.Bytes(), &rejected) != nil || rejected.Error != "identity_changed" {
		t.Fatalf("stale supplied binding = HTTP%d %s", rr.Code, rr.Body.String())
	}
}
