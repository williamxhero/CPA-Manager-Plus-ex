package httpapi

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// These are declared expectations, not evidence of a real browser/profile,
// logged-in account or observed DOM. No browser is invoked by this slice.
const webSourceJSON = `{"site":"layso","accountId":"synthetic-account-a","tenantId":"synthetic-tenant-a","role":"billing-reader","executionUser":"synthetic-operator","browser":{"family":"microsoft-edge","profile":"Profile 1","instanceId":"synthetic-edge-instance"},"currency":"USD","parsingRule":"unverified"}`

func webConfiguration(version int) map[string]any {
	var body map[string]any
	json.Unmarshal([]byte(`{`+balanceBinding+`,"adapter":"layso_billing_web_v1","web":`+webSourceJSON+`}`), &body)
	body["version"] = version
	return body
}

func webBody(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestOfficialBalanceWebConfigurationPersistsWithoutSecret(t *testing.T) {
	f := newOfficialFixture(t)
	rr := testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(0)), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var registered map[string]any
	testutil.DecodeJSON(t, rr, &registered)
	if registered["configured"] != true || registered["version"] != float64(1) || registered["state"] != "unavailable" || registered["reason"] != "web_source_verification_required" || registered["managerDirectQuery"] != false || registered["coreQuery"] != false || registered["coreGate"] != false {
		t.Fatalf("registration must persist without granting query or gate authority: %s", rr.Body.String())
	}
	if registered["endpoint"] != "https://layso.ai/dashboard/models" || !reflect.DeepEqual(registered["web"], webConfiguration(0)["web"]) || registered["attempt"] != nil || registered["lastSuccess"] != nil {
		t.Fatalf("lost source contract or invented observation: %s", rr.Body.String())
	}
	// Reopen the real temporary SQLite file and reconstruct the public API.
	cfg := f.server.AppContext().Config
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	rr = testutil.Request(t, h, "GET", balancePath+"?binding="+url.QueryEscape(`{`+balanceBinding+`}`), "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var read map[string]any
	testutil.DecodeJSON(t, rr, &read)
	if !reflect.DeepEqual(registered, read) || f.count.Load() != 0 {
		t.Fatalf("registration/readback must be durable and perform no collection: %s", rr.Body.String())
	}
}

func TestOfficialBalanceWebUnverifiedSourceCannotStartCollection(t *testing.T) {
	f := newOfficialFixture(t)
	rr := testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(0)), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	rr = testutil.Request(t, f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":1}`, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 400)
	if f.count.Load() != 0 {
		t.Fatal("an unverified web source must not reach the API transport")
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(f.status()), &status); err != nil {
		t.Fatal(err)
	}
	if status["attempt"] != nil || status["lastSuccess"] != nil {
		t.Fatal("rejected collection created an observation", status)
	}
}

func TestOfficialBalanceWebRejectsUnsafeConfiguration(t *testing.T) {
	f := newOfficialFixture(t)
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(0)), testutil.AdminKey), 200)
	before := f.status()
	tests := []struct {
		name string
		edit func(map[string]any)
	}{
		{"missing-contract", func(b map[string]any) { delete(b, "web") }},
		{"null-contract", func(b map[string]any) { b["web"] = nil }},
		{"fake-secret", func(b map[string]any) { b["secret"] = "synthetic-canary-secret" }},
		{"whitespace-secret", func(b map[string]any) { b["secret"] = " " }},
		{"private-permit", func(b map[string]any) { b["privateTargets"] = []string{"127.0.0.1"} }},
		{"arbitrary-url", func(b map[string]any) { b["endpoint"] = "https://example.com/billing" }},
		{"http", func(b map[string]any) { b["endpoint"] = "http://layso.ai/billing" }},
		{"private-api", func(b map[string]any) { b["endpoint"] = "https://layso.ai/api/billing" }},
		{"url-query", func(b map[string]any) { b["endpoint"] = "https://layso.ai/dashboard/models?account=other" }},
		{"url-fragment", func(b map[string]any) { b["endpoint"] = "https://layso.ai/dashboard/models#other" }},
		{"url-userinfo", func(b map[string]any) { b["endpoint"] = "https://user@layso.ai/billing" }},
		{"script", func(b map[string]any) { b["script"] = "document.cookie" }},
		{"selector", func(b map[string]any) { b["selector"] = ".invented" }},
		{"api-contract", func(b map[string]any) { b["newAPI"] = map[string]any{} }},
		{"volcengine-contract", func(b map[string]any) { b["volcengine"] = map[string]any{} }},
		{"aliyun-contract", func(b map[string]any) { b["aliyun"] = map[string]any{} }},
		{"api-with-web-contract", func(b map[string]any) { b["adapter"] = "deepseek" }},
		{"negative-version", func(b map[string]any) { b["version"] = -1 }},
		{"unsafe-version", func(b map[string]any) { b["version"] = 9007199254740991 }},
	}
	for _, field := range []string{"accountId", "tenantId", "role", "executionUser"} {
		for _, value := range []string{"", " synthetic-space", "a\nb", strings.Repeat("a", 129)} {
			field, value := field, value
			tests = append(tests, struct {
				name string
				edit func(map[string]any)
			}{field + "/" + value, func(b map[string]any) { b["web"].(map[string]any)[field] = value }})
		}
	}
	for _, tc := range []struct{ field, value string }{
		{"site", "other"}, {"currency", "CNY"}, {"parsingRule", "invented-dom-v1"},
		{"url", "https://example.com"}, {"script", "document.cookie"}, {"selector", ".invented"},
	} {
		tc := tc
		tests = append(tests, struct {
			name string
			edit func(map[string]any)
		}{"web-" + tc.field, func(b map[string]any) { b["web"].(map[string]any)[tc.field] = tc.value }})
	}
	for _, tc := range []struct{ field, value string }{
		{"family", "google-chrome"}, {"profile", "Default"}, {"instanceId", ""},
		{"instanceId", "invalid instance"}, {"debugUrl", "http://localhost:9222"},
	} {
		tc := tc
		tests = append(tests, struct {
			name string
			edit func(map[string]any)
		}{"browser-" + tc.field + "-" + tc.value, func(b map[string]any) {
			b["web"].(map[string]any)["browser"].(map[string]any)[tc.field] = tc.value
		}})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := webConfiguration(1)
			tc.edit(body)
			rr := testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, body), testutil.AdminKey)
			testutil.RequireStatus(t, rr, 400)
			if strings.Contains(rr.Body.String(), "synthetic-canary-secret") || before != f.status() || f.count.Load() != 0 {
				t.Fatal("invalid input changed durable configuration, leaked a secret or performed collection")
			}
		})
	}
}

func TestOfficialBalanceWebManagementAuthorization(t *testing.T) {
	f := newOfficialFixture(t)
	for _, key := range []string{"", "synthetic-invalid-panel-key"} {
		for _, method := range []string{"GET", "PUT", "POST"} {
			rr := testutil.Request(t, f.handler, method, balancePath+"?binding="+url.QueryEscape(`{`+balanceBinding+`}`), webBody(t, webConfiguration(0)), key)
			testutil.RequireStatus(t, rr, 401)
		}
	}
	if strings.Contains(f.status(), "synthetic-account-a") || f.count.Load() != 0 {
		t.Fatal("unauthorized registration changed the source")
	}
}

func TestOfficialBalanceWebAccountRoleAndExecutionIdentityCAS(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"accountId", "synthetic-account-b"}, {"tenantId", "synthetic-tenant-b"},
		{"role", "billing-owner"}, {"executionUser", "synthetic-operator-b"},
		{"instanceId", "synthetic-edge-instance-b"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			f := newOfficialFixture(t)
			// Seed an actual observation via the existing controlled HTTPS API.
			// Changing to a declared web account must not reuse it or its secret.
			f.configure("")
			if got := f.wait(f.query()); !strings.Contains(got, `"lastSuccess"`) {
				t.Fatal("controlled API seed did not succeed", got)
			}
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(1)), testutil.AdminKey), 200)
			changed := webConfiguration(2)
			web := changed["web"].(map[string]any)
			if tc.field == "instanceId" {
				web["browser"].(map[string]any)[tc.field] = tc.value
			} else {
				web[tc.field] = tc.value
			}
			rr := testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, changed), testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			// Stale account/role writers cannot replace the current declaration.
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(2)), testutil.AdminKey), 409)
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":2}`, testutil.AdminKey), 409)
			var status map[string]any
			if err := json.Unmarshal([]byte(f.status()), &status); err != nil {
				t.Fatal(err)
			}
			if status["version"] != float64(3) || !reflect.DeepEqual(status["web"], web) || status["attempt"] != nil || status["lastSuccess"] != nil || status["coreGate"] != false {
				t.Fatal("changed source inherited old observation/authority", status)
			}
			// Changing back to an API without a secret proves the web transition
			// removed the previous API credential, without inspecting the DB.
			body := `{` + balanceBinding + `,"version":3,"adapter":"deepseek","endpoint":"` + f.upstream.URL + f.path + `","privateTargets":["127.0.0.1"]}`
			rr = testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			if !strings.Contains(rr.Body.String(), `"configured":false`) || strings.Contains(rr.Body.String(), "lastSuccess") || f.count.Load() != 1 {
				t.Fatal("web source retained API credentials/results or collected during configuration", rr.Body.String())
			}
		})
	}
}

func TestOfficialBalanceWebCompleteResourceVersionIsolation(t *testing.T) {
	for _, change := range []struct{ old, fresh string }{
		{`"identity_version":2`, `"identity_version":3`},
		{`"credential_version":3`, `"credential_version":4`},
		{`"config_version":4`, `"config_version":5`},
		{`"membership_version":0`, `"membership_version":1`},
	} {
		t.Run(change.old, func(t *testing.T) {
			f := newOfficialFixture(t)
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(0)), testutil.AdminKey), 200)
			f.coreSnapshot.Store(strings.ReplaceAll(testSnapshotJSON, change.old, change.fresh))
			fresh := strings.ReplaceAll(`{`+balanceBinding+`}`, change.old, change.fresh)
			rr := testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(fresh), "", testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			var status map[string]any
			testutil.DecodeJSON(t, rr, &status)
			if status["configured"] != false || status["web"] != nil || status["attempt"] != nil || status["lastSuccess"] != nil {
				t.Fatal("new resource version inherited old web account or authority", status)
			}
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(`{`+balanceBinding+`}`), "", testutil.AdminKey), 409)
			body := strings.ReplaceAll(webBody(t, webConfiguration(1)), change.old, change.fresh)
			// The sorted JSON marshaler inserts no spaces, so the target-version
			// substitution above changes exactly the host-issued tuple field.
			rr = testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			if !strings.Contains(rr.Body.String(), `"configured":true`) || !strings.Contains(rr.Body.String(), `"version":2`) {
				t.Fatal("fresh tuple could not establish its own configuration", rr.Body.String())
			}
		})
	}
}

func TestOfficialBalanceWebRequiresCompleteSourceGroup(t *testing.T) {
	f := newOfficialFixture(t)
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(testSnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	seed := snapshot["resources"].([]any)[0].(map[string]any)
	var peer map[string]any
	if err := json.Unmarshal([]byte(webBody(t, seed)), &peer); err != nil {
		t.Fatal(err)
	}
	peer["resource_id"] = "66666666-6666-4666-8666-666666666666"
	peer["incarnation"] = "77777777-7777-4777-8777-777777777777"
	body := webConfiguration(0)
	seedTarget := body["targets"].([]any)[0].(map[string]any)
	peerTarget := map[string]any{}
	for k, v := range seedTarget {
		peerTarget[k] = v
	}
	peerTarget["resource_id"], peerTarget["incarnation"] = peer["resource_id"], peer["incarnation"]
	seed["source_bindings"], peer["source_bindings"] = []any{peerTarget}, []any{seedTarget}
	seed["binding_state"], peer["binding_state"] = "verified_projection", "verified_projection"
	snapshot["resources"] = []any{seed, peer}
	f.coreSnapshot.Store(webBody(t, snapshot))
	// A seed target alone cannot authorize the full source projection.
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, body), testutil.AdminKey), 409)
	body["targets"] = []any{seedTarget, peerTarget}
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, body), testutil.AdminKey), 200)

	// Even when the seed is unchanged, a peer/source version change fences the
	// entire declaration. Update the Core source-binding edge and target.
	oldBinding := map[string]any{"scope": body["scope"], "targets": body["targets"]}
	oldJSON := webBody(t, oldBinding)
	peerTarget["config_version"], peer["config_version"] = float64(5), float64(5)
	f.coreSnapshot.Store(webBody(t, snapshot))
	rr := testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(webBody(t, oldBinding)), "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var status map[string]any
	testutil.DecodeJSON(t, rr, &status)
	if status["configured"] != false || status["web"] != nil || status["lastSuccess"] != nil {
		t.Fatal("peer/source version change inherited seed account authority", status)
	}
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "GET", balancePath+"?binding="+url.QueryEscape(oldJSON), "", testutil.AdminKey), 409)
}

func TestOfficialBalanceWebReplacementFencesLateAPIObservation(t *testing.T) {
	f := newOfficialFixture(t)
	f.configure("")
	// Exercise the existing production async worker/version fence with a real
	// controlled TLS response arriving after account and role replacement.
	f.delay.Store(200)
	id := f.query()
	until := time.Now().Add(3 * time.Second)
	for f.count.Load() == 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if f.count.Load() != 1 {
		t.Fatal("controlled API request never started")
	}
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(1)), testutil.AdminKey), 200)
	changed := webConfiguration(2)
	changed["web"].(map[string]any)["accountId"] = "synthetic-account-b"
	changed["web"].(map[string]any)["role"] = "billing-owner"
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, changed), testutil.AdminKey), 200)
	// Wait beyond the controlled server response delay, as in the existing
	// public API source-change regression. No browser or site is involved.
	time.Sleep(300 * time.Millisecond)
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "GET", balancePath+"?job="+id, "", testutil.AdminKey), 409)
	var status map[string]any
	if err := json.Unmarshal([]byte(f.status()), &status); err != nil {
		t.Fatal(err)
	}
	if status["version"] != float64(3) || status["attempt"] != nil || status["lastSuccess"] != nil || !reflect.DeepEqual(status["web"], changed["web"]) || f.count.Load() != 1 {
		t.Fatal("late API result crossed the source/account/role version fence", status)
	}
}
