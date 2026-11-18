package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Core's source projection is a set, not an ordered list. A different map
// iteration order must not hide persisted OpenCode 5h quotas from the panel.
func TestCredentialQuotaBatchStatusesSourceOrder(t *testing.T) {
	for _, changeIdentity := range []bool{false, true} {
		name := "reordered-sources-preserve-five-hour"
		if changeIdentity {
			name = "reordered-sources-still-reject-identity-change"
		}
		t.Run(name, func(t *testing.T) {
			replaceIdentity := func(value string) string {
				return strings.NewReplacer(
					"44444444-4444-4444-8444-444444444444", "66666666-6666-4666-8666-666666666666",
					"55555555-5555-4555-8555-555555555555", "77777777-7777-4777-8777-777777777777",
					"0123456789abcdef", "fedcba9876543210",
					"source-view-id", "second-source-id",
					"subject-view-id", "second-subject-id",
				).Replace(value)
			}
			first := strings.Replace(sourceViewSource, "codex-oauth-wham", "opencode-go-usage", 1)
			second := replaceIdentity(first)
			resource := strings.Replace(testSnapshotJSON, `"family":"codex"`, `"family":"opencode-go"`, 1)
			start := strings.Index(resource, `"resources":[`) + len(`"resources":[`)
			end := strings.LastIndex(resource, `]}`)
			row := resource[start:end]
			options := sourceViewContractCase{
				snapshot: resource[:start] + row + "," + replaceIdentity(row) + resource[end:],
				view: func(n int) string {
					sources := first + "," + second
					if n%2 == 0 {
						closing := first
						if changeIdentity {
							closing = strings.Replace(closing, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
						}
						sources = second + "," + closing
					}
					return `{"version":1,"sources":[` + sources + `],"bindings":[` + sourceViewBinding + `,` + replaceIdentity(sourceViewBinding) + `]}`
				},
			}
			server, paths := sourceViewContractServer(t, options)
			var source cq.Source
			if err := json.Unmarshal([]byte(first), &source); err != nil {
				t.Fatal(err)
			}
			ids, _ := json.Marshal([]string{source.Scope.InstanceID, source.Scope.InstanceIncarnation, source.Targets[0].ResourceID, source.Targets[0].Incarnation})
			sum := sha256.Sum256(ids)
			id := hex.EncodeToString(sum[:])
			observation, err := json.Marshal(map[string]any{
				"queryId": "order-job", "queryStatus": "success", "observedAtMs": 100,
				"windowsJson": `{"windows":[{"name":"rolling","usedPercent":0,"remainingFraction":1}]}`,
			})
			if err != nil {
				t.Fatal(err)
			}
			st := server.AppContext().CredentialQuotaService.Store
			ctx := context.Background()
			if err := st.BeginCredentialQuota(ctx, store.CredentialQuotaRecord{ID: id, Version: 1, Source: first, Attempt: string(observation)}, 0, "order-job", `{}`, false); err != nil {
				t.Fatal(err)
			}
			if err := st.CompleteCredentialQuota(ctx, id, 1, "order-job", string(observation), `{}`, true); err != nil {
				t.Fatal(err)
			}

			rr := testutil.Request(t, server.Handler(), "GET", "/usage-service/credential-quota?statuses=1", "", testutil.AdminKey)
			if changeIdentity {
				testutil.RequireStatus(t, rr, http.StatusConflict)
			} else {
				testutil.RequireStatus(t, rr, http.StatusOK)
				var response struct {
					Statuses []cq.Status `json:"statuses"`
				}
				testutil.DecodeJSON(t, rr, &response)
				if len(response.Statuses) != 2 || response.Statuses[0].AuthIndex != source.AuthIndex || response.Statuses[0].LastSuccess == nil || !strings.Contains(response.Statuses[0].LastSuccess.WindowsJSON, `"name":"rolling"`) {
					t.Fatalf("reordering dropped OpenCode 5h evidence: %s", rr.Body.String())
				}
			}
			for _, path := range paths() {
				if path == "/v8/management/credential-quota/query" {
					t.Fatal("batch read started a provider query")
				}
			}
		})
	}
}
