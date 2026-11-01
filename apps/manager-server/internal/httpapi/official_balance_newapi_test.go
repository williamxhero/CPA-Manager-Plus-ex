package httpapi

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

const newAPISelfContract = `"newAPI":{"protocolConfirmed":true,"instanceVersion":"operator-confirmed-synthetic-v1","auth":"user-pat-with-user-id-v1","userID":"42","accountScope":"synthetic-account-A","nativeUnit":"newapi_quota"}`

func newAPIConfigure(f *officialFixture, contract string, version int, secret string) string {
	f.t.Helper()
	body := `{` + balanceBinding + `,"version":` + jsonNumber(version) + `,"adapter":"` + f.adapter + `","endpoint":"` + f.upstream.URL + f.path + `","privateTargets":["127.0.0.1"],` + contract + `,"secret":"` + secret + `"}`
	rr := testutil.Request(f.t, f.handler, "PUT", balancePath, body, testutil.AdminKey)
	testutil.RequireStatus(f.t, rr, 200)
	return rr.Body.String()
}

func TestOfficialBalanceNewAPITokenAndFailures(t *testing.T) {
	f := newOfficialFixture(t)
	f.adapter = "newapi-token-usage-v1"
	f.path = "/api/usage/token/"
	newAPIConfigure(f, `"newAPI":{"protocolConfirmed":true,"instanceVersion":"synthetic-token-v1","auth":"inference-key-readonly-v1","nativeUnit":"newapi_quota"}`, 0, "synthetic-query-canary")
	for _, tc := range []struct{ body, state, remaining string }{
		{`{"code":true,"data":{"object":"token_usage","unlimited_quota":false,"total_available":"9007199254740993123456789","user_quota":{"total_available":999}}}`, "finite", "9007199254740993123456789"},
		{`{"code":true,"data":{"object":"token_usage","unlimited_quota":true,"total_available":100000000}}`, "unlimited", ""},
		{`{"code":true,"data":{"object":"token_usage","total_available":0}}`, "unknown", ""},
	} {
		f.response.Store(tc.body)
		got := f.wait(f.query())
		var job officialbalance.Job
		if json.Unmarshal([]byte(got), &job) != nil || job.Status == nil || job.Status.LastSuccess == nil {
			t.Fatal(got)
		}
		obs := job.Status.LastSuccess
		if obs.NativeQuota == nil || obs.NativeQuota.State != tc.state || obs.Semantic != "key_remaining" || obs.AmountDecimal != "" || obs.Currency != "" {
			t.Fatal(got)
		}
		if tc.remaining == "" && obs.NativeQuota.Remaining != nil || tc.remaining != "" && (obs.NativeQuota.Remaining == nil || *obs.NativeQuota.Remaining != tc.remaining) {
			t.Fatal(got)
		}
	}
}

func TestOfficialBalanceNewAPIFailuresRetainLastSuccess(t *testing.T) {
	f := newOfficialFixture(t)
	f.adapter = "newapi-user-self-v1"
	f.path = "/api/user/self"
	newAPIConfigure(f, newAPISelfContract, 0, "synthetic-query-canary")
	f.response.Store(`{"success":true,"data":{"id":42,"quota":-17}}`)
	f.wait(f.query())
	for _, tc := range []struct{ body, state string }{
		{"status:401", "auth_error"}, {"status:403", "auth_error"}, {"status:429", "rate_limited"},
		{`<html>synthetic-query-canary</html>`, "parse_error"},
		{`{"success":true,"data":{"id":41,"quota":999}}`, "parse_error"},
		{`{"success":true,"data":{"id":42,"quota":null}}`, "parse_error"},
		{`{"success":false,"ſuccess":true,"data":{"id":42,"quota":0}}`, "parse_error"},
		{`{"success":true,"data":{"id":42,"quota":1},"padding":"` + strings.Repeat("x", 2<<20) + `"}`, "parse_error"},
	} {
		f.response.Store(tc.body)
		raw := f.wait(f.query())
		var job officialbalance.Job
		if json.Unmarshal([]byte(raw), &job) != nil || job.Status == nil || job.Status.State != tc.state || !job.Status.Stale || job.Status.LastSuccess == nil || job.Status.LastSuccess.NativeQuota == nil || job.Status.LastSuccess.NativeQuota.Remaining == nil || *job.Status.LastSuccess.NativeQuota.Remaining != "-17" || job.Status.Attempt.NativeQuota != nil || strings.Contains(raw, "synthetic-query-canary") {
			t.Fatal("failed reading did not retain native success safely:", raw)
		}
	}
}

func TestOfficialBalanceNewAPIAuthorityChangesClearSecretAndCAS(t *testing.T) {
	for _, tc := range []struct{ name, old, new string }{
		{"account", "synthetic-account-A", "synthetic-account-B"},
		{"userID", `"userID":"42"`, `"userID":"43"`},
		{"auth", "user-pat-with-user-id-v1", "user-pat-v1"},
		{"version", "operator-confirmed-synthetic-v1", "operator-confirmed-synthetic-v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOfficialFixture(t)
			f.adapter = "newapi-user-self-v1"
			f.path = "/api/user/self"
			newAPIConfigure(f, newAPISelfContract, 0, "synthetic-query-canary")
			// An unchanged explicit contract preserves; an authority change clears.
			if got := newAPIConfigure(f, newAPISelfContract, 1, ""); !strings.Contains(got, `"configured":true`) {
				t.Fatal(got)
			}
			changed := strings.Replace(newAPISelfContract, tc.old, tc.new, 1)
			if got := newAPIConfigure(f, changed, 2, ""); !strings.Contains(got, `"configured":false`) {
				t.Fatal("PAT reused across contract authority:", got)
			}
			n := f.count.Load()
			rr := testutil.Request(t, f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":3}`, testutil.AdminKey)
			testutil.RequireStatus(t, rr, 202)
			var job officialbalance.Job
			json.Unmarshal(rr.Body.Bytes(), &job)
			f.wait(job.ID)
			if f.count.Load() != n {
				t.Fatal("changed authority without secret made an upstream request")
			}
			if got := newAPIConfigure(f, changed, 3, "synthetic-query-canary"); !strings.Contains(got, `"configured":true`) {
				t.Fatal(got)
			}
			stale := `{` + balanceBinding + `,"version":3,"adapter":"newapi-user-self-v1","endpoint":"` + f.upstream.URL + f.path + `",` + changed + `}`
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, stale, testutil.AdminKey), 409)
		})
	}
}

func TestOfficialBalanceNewAPILateAccountResultFenced(t *testing.T) {
	f := newOfficialFixture(t)
	f.adapter = "newapi-user-self-v1"
	f.path = "/api/user/self"
	entered, release, finished := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1)
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("New-Api-User") == "42" {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `{"success":true,"data":{"id":42,"quota":999}}`)
			finished <- struct{}{}
			return
		}
		if r.Header.Get("New-Api-User") != "43" {
			t.Error("wrong account contract")
		}
		fmt.Fprint(w, `{"success":true,"data":{"id":43,"quota":7}}`)
	}))
	defer up.Close()
	f.upstream = up
	pool := x509.NewCertPool()
	pool.AddCert(up.Certificate())
	f.server.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: pool})
	newAPIConfigure(f, newAPISelfContract+`,"timeoutMs":3000`, 0, "synthetic-query-canary")
	old := f.query()
	oldRead := testutil.Request(t, f.handler, "GET", balancePath+"?job="+old, "", testutil.AdminKey)
	testutil.RequireStatus(t, oldRead, 200)
	var oldJob officialbalance.Job
	if json.Unmarshal(oldRead.Body.Bytes(), &oldJob) != nil {
		t.Fatal("missing persisted old attempt deadline")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("old account never queried")
	}
	changed := strings.Replace(strings.Replace(newAPISelfContract, `"userID":"42"`, `"userID":"43"`, 1), "synthetic-account-A", "synthetic-account-B", 1)
	newAPIConfigure(f, changed+`,"timeoutMs":3000`, 1, "synthetic-query-canary")
	rr := testutil.Request(t, f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":2}`, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 202)
	var current officialbalance.Job
	json.Unmarshal(rr.Body.Bytes(), &current)
	if got := f.wait(current.ID); !strings.Contains(got, `"remaining":"7"`) {
		t.Fatal(got)
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("old response did not finish")
	}
	if remaining := time.Until(time.UnixMilli(oldJob.DeadlineMS)); remaining > 0 {
		time.Sleep(remaining + 20*time.Millisecond)
	}
	// A fresh persisted current job remains authoritative while the old job is
	// unreadable across source version. Late old result must not reappear.
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "GET", balancePath+"?job="+old, "", testutil.AdminKey), 409)
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		got := f.status()
		if strings.Contains(got, `"remaining":"999"`) || !strings.Contains(got, `"remaining":"7"`) {
			t.Fatal("late account result overwrote source:", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Reconstructed host reads the same fenced SQLite observation, not a cache.
	f.handler = New(f.server.AppContext().Config, f.db, collector.NewManager(f.server.AppContext().Config, f.db)).Handler()
	if got := f.status(); !strings.Contains(got, `"remaining":"7"`) {
		t.Fatal(got)
	}
}

func TestOfficialBalanceNewAPICapabilities(t *testing.T) {
	caps, err := balance.New().ListCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{balance.NewAPIUserSelfV1, balance.NewAPITokenUsageV1} {
		found := false
		for _, c := range caps {
			if c.Adapter == id && c.Supported && c.RequiresConfig {
				found = true
			}
		}
		if !found {
			t.Fatal("missing explicit adapter capability:", id)
		}
	}
}

func TestOfficialBalanceNewAPIMissingContractAndUnimplementedConversionRejected(t *testing.T) {
	f := newOfficialFixture(t)
	f.adapter = "newapi-user-self-v1"
	f.path = "/api/user/self"
	newAPIConfigure(f, newAPISelfContract, 0, "synthetic-query-canary")
	for _, contract := range []string{
		strings.Replace(newAPISelfContract, `"protocolConfirmed":true`, `"protocolConfirmed":false`, 1),
		strings.Replace(newAPISelfContract, `"instanceVersion":"operator-confirmed-synthetic-v1"`, `"instanceVersion":""`, 1),
		strings.Replace(newAPISelfContract, `"accountScope":"synthetic-account-A"`, `"accountScope":""`, 1),
		strings.Replace(newAPISelfContract, `"userID":"42"`, `"userID":""`, 1),
		strings.Replace(newAPISelfContract, `"nativeUnit":"newapi_quota"`, `"nativeUnit":"USD"`, 1),
		strings.Replace(newAPISelfContract, `"nativeUnit":"newapi_quota"`, `"nativeUnit":"newapi_quota","currency":"USD","quotaToCurrencyMultiplier":"0.000002"`, 1),
	} {
		body := `{` + balanceBinding + `,"version":1,"adapter":"` + f.adapter + `","endpoint":"` + f.upstream.URL + f.path + `",` + contract + `,"secret":"synthetic-query-canary"}`
		testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 400)
	}
	for _, method := range []string{"GET", "PUT", "POST"} {
		testutil.RequireStatus(t, testutil.Request(t, f.handler, method, balancePath, `{`+balanceBinding+`}`, ""), 401)
	}
	if status := f.status(); !strings.Contains(status, `"configured":true`) || !strings.Contains(status, `"version":1`) || f.count.Load() != 0 {
		t.Fatal("invalid contract changed source or queried upstream:", status)
	}
}

func jsonNumber(n int) string { raw, _ := json.Marshal(n); return string(raw) }

func TestOfficialBalanceNewAPISelfPersistedNativeDebt(t *testing.T) {
	f := newOfficialFixture(t)
	f.adapter = "newapi-user-self-v1"
	f.path = "/api/user/self"
	f.response.Store(`{"success":true,"data":{"id":42,"quota":-9007199254740993}}`)
	configured := newAPIConfigure(f, newAPISelfContract, 0, "synthetic-query-canary")
	if !strings.Contains(configured, `"accountScope":"synthetic-account-A"`) || !strings.Contains(configured, `"managerDirectQuery":true`) || strings.Contains(configured, "synthetic-query-canary") {
		t.Fatal("contract missing or secret echoed:", configured)
	}
	got := f.wait(f.query())
	if !strings.Contains(got, `"remaining":"-9007199254740993"`) || !strings.Contains(got, `"nativeQuota"`) || !strings.Contains(got, `"semantic":"account_wallet"`) || strings.Contains(got, `"currency"`) {
		t.Fatal("native debt not persisted:", got)
	}
	count := f.count.Load()
	f.handler = New(f.server.AppContext().Config, f.db, collector.NewManager(f.server.AppContext().Config, f.db)).Handler()
	if status := f.status(); !strings.Contains(status, `"remaining":"-9007199254740993"`) || f.count.Load() != count {
		t.Fatal("SQLite read/restart lost native quota or queried upstream:", status)
	}
	var job officialbalance.Job
	if json.Unmarshal([]byte(got), &job) != nil || job.Status == nil || job.Status.Stale {
		t.Fatal("invalid persisted status")
	}
}

func TestOfficialBalanceNewAPIUIFixture(t *testing.T) {
	dir := os.Getenv("NEWAPI_BALANCE_UI_FIXTURE")
	if dir == "" {
		t.Skip("explicit synthetic NewAPI UI fixture")
	}
	f := newOfficialFixture(t)
	f.adapter = "newapi-user-self-v1"
	f.path = "/api/user/self"
	binding := fullOfficialUIBinding(t, f)
	f.response.Store(`{"success":true,"data":{"id":42,"quota":-9007199254740993}}`)
	// Both explicitly selected protocol paths share one synthetic instance. No
	// arbitrary path or second HTTP client is substituted for production safety.
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.count.Add(1)
		if r.Method != "GET" || (r.URL.Path != "/api/user/self" && r.URL.Path != "/api/usage/token/") || r.Header.Get("Authorization") != "Bearer synthetic-query-canary" {
			t.Error("invalid explicit NewAPI UI request")
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/api/user/self" && r.Header.Get("New-Api-User") != "42" || r.URL.Path == "/api/usage/token/" && r.Header.Get("New-Api-User") != "" {
			t.Error("wrong UI auth contract")
			w.WriteHeader(400)
			return
		}
		body := f.response.Load().(string)
		if strings.HasPrefix(body, "status:") {
			var status int
			fmt.Sscanf(body, "status:%d", &status)
			w.WriteHeader(status)
			fmt.Fprint(w, "synthetic-query-canary")
			return
		}
		fmt.Fprint(w, body)
	}))
	defer up.Close()
	f.upstream = up
	pool := x509.NewCertPool()
	pool.AddCert(up.Certificate())
	f.server.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: pool})
	manager := officialBrowserServer(t, f.handler)
	defer manager.Close()
	control := officialBrowserServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var s struct{ Response string }
		if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&s) != nil {
			w.WriteHeader(400)
			return
		}
		f.response.Store(s.Response)
		w.WriteHeader(204)
	}))
	defer control.Close()
	raw, _ := json.Marshal(map[string]any{"base": manager.URL, "binding": binding, "managementKey": testutil.AdminKey, "endpoint": f.upstream.URL + f.path, "tokenEndpoint": f.upstream.URL + "/api/usage/token/", "control": control.URL})
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2400; i++ {
		if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("UI fixture not stopped within 240 seconds")
}
