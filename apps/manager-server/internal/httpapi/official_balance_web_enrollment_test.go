package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const enrollmentPath = balancePath + "/web-enrollment"

func enrollmentRequest(t *testing.T, version int, authority string) map[string]any {
	t.Helper()
	body := webConfiguration(0)
	return map[string]any{
		"scope": body["scope"], "targets": body["targets"], "version": 1,
		"sourceAuthority": authority, "registrationVersion": version,
		"proofReference": "controlled-review-reference-1",
	}
}

func enrollmentRead(t *testing.T, handler http.Handler) map[string]any {
	t.Helper()
	rr := testutil.Request(t, handler, "GET", enrollmentPath+"?binding="+url.QueryEscape(`{`+balanceBinding+`}`), "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var result map[string]any
	testutil.DecodeJSON(t, rr, &result)
	return result
}

// The production runtime, controller and SQLite are unchanged by fixtures.
// The reference is a request for review, not verified browser/parser evidence.
func TestOfficialBalanceWebEnrollmentRegistrationPersistsPending(t *testing.T) {
	f := newOfficialFixture(t)
	rr := testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(0)), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var source map[string]any
	testutil.DecodeJSON(t, rr, &source)
	body := enrollmentRequest(t, 0, source["webSourceAuthority"].(string))
	rr = testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, body), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var saved map[string]any
	testutil.DecodeJSON(t, rr, &saved)
	if saved["state"] != "pending_verification" || saved["registrationVersion"] != float64(1) || saved["proofReference"] != body["proofReference"] || saved["collectionAuthorized"] != false || saved["parserSupported"] != false {
		t.Fatalf("registration must not self-certify a browser or parser: %s", rr.Body.String())
	}
	// Reopen the same temporary SQLite; assert through the public API only.
	cfg := f.server.AppContext().Config
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	protector, _ := security.NewProtector([]byte("0123456789abcdef0123456789abcdef"))
	db, err := store.Open(cfg.DBPath, protector)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(cfg, db, collector.NewManager(cfg, db)).Handler()
	if got := enrollmentRead(t, h); !reflect.DeepEqual(saved, got) {
		t.Fatalf("registration changed after SQLite reopen: saved=%v got=%v", saved, got)
	}
	testutil.RequireStatus(t, testutil.Request(t, h, "POST", balancePath, `{`+balanceBinding+`,"version":1}`, testutil.AdminKey), 400)
	if f.count.Load() != 0 {
		t.Fatal("pending enrollment reached an external balance transport")
	}
}

func registerPending(t *testing.T, f *officialFixture) map[string]any {
	t.Helper()
	rr := testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(0)), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var source map[string]any
	testutil.DecodeJSON(t, rr, &source)
	body := enrollmentRequest(t, 0, source["webSourceAuthority"].(string))
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, body), testutil.AdminKey), 200)
	return body
}

func TestOfficialBalanceWebEnrollmentCannotSelfCertifyOrExecute(t *testing.T) {
	f := newOfficialFixture(t)
	body := registerPending(t, f)
	before := enrollmentRead(t, f.handler)
	for _, field := range []string{"profileVerified", "executionUserVerified", "parserVerified", "evidenceKind", "script", "selector", "rules", "url"} {
		t.Run(field, func(t *testing.T) {
			invalid := enrollmentRequest(t, 1, body["sourceAuthority"].(string))
			invalid[field] = "synthetic-untrusted-assertion"
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, invalid), testutil.AdminKey), 400)
		})
	}
	for _, reference := range []string{"", "https://layso.ai/billing", "../secret", "a\nb", strings.Repeat("a", 129)} {
		t.Run("reference-"+reference, func(t *testing.T) {
			invalid := enrollmentRequest(t, 1, body["sourceAuthority"].(string))
			invalid["proofReference"] = reference
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, invalid), testutil.AdminKey), 400)
		})
	}
	if got := enrollmentRead(t, f.handler); !reflect.DeepEqual(before, got) {
		t.Fatal("invalid request changed registration", got)
	}
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":1}`, testutil.AdminKey), 400)
	if strings.Contains(f.status(), "lastSuccess") || f.count.Load() != 0 {
		t.Fatal("untrusted enrollment acquired collection or invented an observation")
	}
}

func TestOfficialBalanceWebEnrollmentAuthorization(t *testing.T) {
	f := newOfficialFixture(t)
	body := registerPending(t, f)
	before := enrollmentRead(t, f.handler)
	for _, key := range []string{"", "synthetic-invalid-panel-key"} {
		for _, method := range []string{"GET", "PUT"} {
			rr := testutil.Request(t, f.handler, method, enrollmentPath+"?binding="+url.QueryEscape(`{`+balanceBinding+`}`), webBody(t, body), key)
			testutil.RequireStatus(t, rr, 401)
		}
	}
	if !reflect.DeepEqual(before, enrollmentRead(t, f.handler)) {
		t.Fatal("unauthorized request changed registration")
	}
}

func TestOfficialBalanceWebEnrollmentWithdrawalAndCAS(t *testing.T) {
	f := newOfficialFixture(t)
	body := registerPending(t, f)
	// A delayed initial writer cannot replace the request or resurrect it.
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, body), testutil.AdminKey), 409)
	body["registrationVersion"], body["withdraw"], body["proofReference"] = 1, true, ""
	rr := testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, body), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	read := enrollmentRead(t, f.handler)
	if read["state"] != "not_registered" || read["registrationVersion"] != float64(2) || read["proofReference"] != nil || read["collectionAuthorized"] != false {
		t.Fatal("withdrawal left a usable request or discarded CAS tombstone", read)
	}
	body["withdraw"], body["proofReference"] = false, "controlled-review-reference-2"
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, body), testutil.AdminKey), 409)
	body["registrationVersion"] = 2
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, body), testutil.AdminKey), 200)
}

func TestOfficialBalanceWebEnrollmentSourceChangesFenceReferences(t *testing.T) {
	for _, field := range []string{"accountId", "tenantId", "role", "executionUser", "browser"} {
		t.Run(field, func(t *testing.T) {
			f := newOfficialFixture(t)
			old := registerPending(t, f)
			changed := webConfiguration(1)
			web := changed["web"].(map[string]any)
			if field == "browser" {
				web[field].(map[string]any)["instanceId"] = "synthetic-edge-instance-b"
			} else {
				web[field] = "synthetic-new-" + field
			}
			rr := testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, changed), testutil.AdminKey)
			testutil.RequireStatus(t, rr, 200)
			var source map[string]any
			testutil.DecodeJSON(t, rr, &source)
			read := enrollmentRead(t, f.handler)
			if read["state"] != "not_registered" || read["proofReference"] != nil || read["registrationVersion"] != float64(1) {
				t.Fatal("new source inherited a pending reference", read)
			}
			old["registrationVersion"] = 1
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, old), testutil.AdminKey), 409)
			old["version"], old["sourceAuthority"] = 2, source["webSourceAuthority"]
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, old), testutil.AdminKey), 200)
		})
	}
}

func TestOfficialBalanceWebEnrollmentCoreIdentityFences(t *testing.T) {
	for _, change := range []struct{ old, fresh string }{
		{`"identity_version":2`, `"identity_version":3`},
		{`"credential_version":3`, `"credential_version":4`},
		{`"config_version":4`, `"config_version":5`},
		{`"membership_version":0`, `"membership_version":1`},
		{`33333333-3333-4333-8333-333333333333`, `aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa`},
	} {
		t.Run(change.old, func(t *testing.T) {
			f := newOfficialFixture(t)
			old := registerPending(t, f)
			f.coreSnapshot.Store(strings.ReplaceAll(testSnapshotJSON, change.old, change.fresh))
			f.coreCaps.Store(strings.ReplaceAll(testCapsJSON, change.old, change.fresh))
			get := enrollmentPath + "?binding=" + url.QueryEscape(`{`+balanceBinding+`}`)
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "GET", get, "", testutil.AdminKey), 409)
			old["registrationVersion"] = 1
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, old), testutil.AdminKey), 409)
		})
	}
}

func TestOfficialBalanceWebEnrollmentConcurrentWritersCAS(t *testing.T) {
	f := newOfficialFixture(t)
	rr := testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, webConfiguration(0)), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var source map[string]any
	testutil.DecodeJSON(t, rr, &source)
	// Independent service instances share SQLite, so a service mutex cannot
	// supply the cross-instance CAS guarantee under test.
	other := New(f.server.AppContext().Config, f.db, collector.NewManager(f.server.AppContext().Config, f.db)).Handler()
	body := webBody(t, enrollmentRequest(t, 0, source["webSourceAuthority"].(string)))
	start := make(chan struct{})
	codes := make(chan int, 2)
	var done sync.WaitGroup
	for _, handler := range []http.Handler{f.handler, other} {
		done.Add(1)
		go func() {
			defer done.Done()
			<-start
			codes <- testutil.Request(t, handler, "PUT", enrollmentPath, body, testutil.AdminKey).Code
		}()
	}
	close(start)
	done.Wait()
	a, b := <-codes, <-codes
	if !(a == 200 && b == 409 || a == 409 && b == 200) {
		t.Fatalf("CAS writers returned %d and %d", a, b)
	}
	if enrollmentRead(t, f.handler)["registrationVersion"] != float64(1) {
		t.Fatal("two writers both changed registration")
	}
}

func TestOfficialBalanceWebEnrollmentTargetReorderingPreservesRegistration(t *testing.T) {
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
	body["targets"] = []any{seedTarget, peerTarget}
	rr := testutil.Request(t, f.handler, "PUT", balancePath, webBody(t, body), testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var source map[string]any
	testutil.DecodeJSON(t, rr, &source)
	request := map[string]any{"scope": body["scope"], "targets": body["targets"], "version": 1,
		"sourceAuthority": source["webSourceAuthority"], "registrationVersion": 0, "proofReference": "controlled-group-review-1"}
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, request), testutil.AdminKey), 200)
	request["targets"] = []any{peerTarget, seedTarget}
	binding := map[string]any{"scope": request["scope"], "targets": request["targets"]}
	rr = testutil.Request(t, f.handler, "GET", enrollmentPath+"?binding="+url.QueryEscape(webBody(t, binding)), "", testutil.AdminKey)
	testutil.RequireStatus(t, rr, 200)
	var reordered map[string]any
	testutil.DecodeJSON(t, rr, &reordered)
	if reordered["state"] != "pending_verification" || reordered["proofReference"] != request["proofReference"] || reordered["sourceAuthority"] != source["webSourceAuthority"] {
		t.Fatalf("valid target reordering changed authority: %s", rr.Body.String())
	}
	request["registrationVersion"] = 1
	request["proofReference"] = "controlled-group-review-2"
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", enrollmentPath, webBody(t, request), testutil.AdminKey), 200)
}
