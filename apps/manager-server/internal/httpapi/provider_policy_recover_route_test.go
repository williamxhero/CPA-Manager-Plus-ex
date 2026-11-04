package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestProviderPolicyRecoverRouteRejectsInvalidBodies(t *testing.T) {
	handler := newTestHandler(t, "", false)
	req := httptest.NewRequest(http.MethodPost, "/usage-service/provider-policy/recover", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != "invalid_provider_recovery" {
		t.Fatalf("body=%s", rec.Body.String())
	}
	get := httptest.NewRequest(http.MethodGet, "/usage-service/provider-policy/recover", nil)
	get.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET recover status=%d", rec.Code)
	}
}
