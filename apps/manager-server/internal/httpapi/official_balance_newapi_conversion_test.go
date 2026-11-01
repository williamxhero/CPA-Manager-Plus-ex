package httpapi

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOfficialBalanceNewAPIConversionProofLateResultFenced(t *testing.T) {
	f := newOfficialFixture(t)
	f.adapter = "newapi-user-self-v1"
	f.path = "/api/user/self"
	entered, release, finished := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1)
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("New-Api-User") != "42" {
			t.Error("wrong stable account")
			w.WriteHeader(401)
			return
		}
		switch r.Header.Get("Authorization") {
		case "Bearer synthetic-old-conversion-secret":
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `{"success":true,"data":{"id":42,"quota":999}}`)
			finished <- struct{}{}
		case "Bearer synthetic-new-conversion-secret":
			fmt.Fprint(w, `{"success":true,"data":{"id":42,"quota":7}}`)
		default:
			t.Error("old conversion credential crossed proof authority")
			w.WriteHeader(401)
		}
	}))
	defer up.Close()
	f.upstream = up
	pool := x509.NewCertPool()
	pool.AddCert(up.Certificate())
	f.server.AppContext().OfficialBalanceService.Querier = balance.NewWithNetwork(balance.NetworkOptions{RootCAs: pool})
	contract := withNewAPIConversion(newAPISelfContract, newAPIConversionProfile) + `,"timeoutMs":3000`
	newAPIConfigure(f, contract, 0, "synthetic-old-conversion-secret")
	old := f.query()
	oldRead := testutil.Request(t, f.handler, "GET", balancePath+"?job="+old, "", testutil.AdminKey)
	var oldJob officialbalance.Job
	json.Unmarshal(oldRead.Body.Bytes(), &oldJob)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("old proof query never entered")
	}
	changed := strings.Replace(strings.Replace(contract, "synthetic-operator-proof-1", "synthetic-operator-proof-2", 1), "0.000002", "0.25", 1)
	newAPIConfigure(f, changed, 1, "synthetic-new-conversion-secret")
	rr := testutil.Request(t, f.handler, "POST", balancePath, `{`+balanceBinding+`,"version":2}`, testutil.AdminKey)
	testutil.RequireStatus(t, rr, 202)
	var fresh officialbalance.Job
	json.Unmarshal(rr.Body.Bytes(), &fresh)
	if got := f.wait(fresh.ID); !strings.Contains(got, `"amountDecimal":"1.75"`) || !strings.Contains(got, `"proofReference":"synthetic-operator-proof-2"`) {
		t.Fatal("new proof contract did not produce new exact amount:", got)
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
	testutil.RequireStatus(t, testutil.Request(t, f.handler, "GET", balancePath+"?job="+old, "", testutil.AdminKey), 409)
	if got := f.status(); !strings.Contains(got, `"amountDecimal":"1.75"`) || strings.Contains(got, `"remaining":"999"`) || strings.Contains(got, "synthetic-old-conversion-secret") || strings.Contains(got, "synthetic-new-conversion-secret") {
		t.Fatal("late proof result contaminated current source:", got)
	}
}

func TestOfficialBalanceNewAPIConversionInvalidProfilesNeverQuery(t *testing.T) {
	f := newOfficialFixture(t)
	f.adapter = "newapi-user-self-v1"
	f.path = "/api/user/self"
	newAPIConfigure(f, newAPISelfContract, 0, "synthetic-query-canary")
	profiles := []string{"{}", "null", `{"confirmed":true}`, strings.Replace(newAPIConversionProfile, `"confirmed":true`, `"confirmed":false`, 1)}
	for _, rate := range []string{`"0"`, `"0.000"`, `"-1"`, `"NaN"`, `"Infinity"`, `"1e-6"`, `" 0.1"`, `"01"`, `null`, `0.000002`, `true`, `"` + strings.Repeat("9", 121) + `"`, `"0.` + strings.Repeat("0", 119) + `1"`} {
		profiles = append(profiles, strings.Replace(newAPIConversionProfile, `"moneyPerNativeQuota":"0.000002"`, `"moneyPerNativeQuota":`+rate, 1))
	}
	for _, change := range []struct{ old, new string }{
		{`"currency":"CNY"`, `"currency":"usd"`}, {`"currency":"CNY"`, `"currency":""`},
		{`"nativeUnit":"newapi_quota"`, `"nativeUnit":"USD"`},
		{`"proofReference":"synthetic-operator-proof-1"`, `"proofReference":""`},
		{`"proofReference":"synthetic-operator-proof-1"`, `"proofReference":"https://do-not-fetch.invalid"`},
		{`"contractVersion":"synthetic-conversion-v1"`, `"contractVersion":""`},
		{`"contractVersion":"synthetic-conversion-v1"`, `"contractVersion":"` + strings.Repeat("v", 129) + `"`},
		{`"confirmed":true`, `"confirmed":true,"currency":"USD"`},
	} {
		profiles = append(profiles, strings.Replace(newAPIConversionProfile, change.old, change.new, 1))
	}
	for _, profile := range profiles {
		body := `{` + balanceBinding + `,"version":1,"adapter":"` + f.adapter + `","endpoint":"` + f.upstream.URL + f.path + `",` + withNewAPIConversion(newAPISelfContract, profile) + `}`
		testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, body, testutil.AdminKey), 400)
	}
	if got := f.status(); !strings.Contains(got, `"version":1`) || !strings.Contains(got, `"configured":true`) || f.count.Load() != 0 {
		t.Fatal("invalid conversion mutated authority or reached upstream:", got)
	}
}

func TestOfficialBalanceNewAPIConversionExactArithmeticAndBounds(t *testing.T) {
	for _, tc := range []struct{ name, quota, rate, want string }{
		{"debt", "-7", "0.125", "-0.875"},
		{"large", "12345678901234567890", "0.000000001", "12345678901.23456789"},
		{"subcent", "3", "0.000000000000000000000000000001", "0.000000000000000000000000000003"},
		{"zero", "0", "0.001", "0"},
		{"trailing-rate-scale", "125", "0.0100", "1.25"},
		{"max-native", strings.Repeat("9", 120), "1", strings.Repeat("9", 120)},
		{"max-rate", "1", strings.Repeat("9", 120), strings.Repeat("9", 120)},
		{"max-fraction", "1", "0." + strings.Repeat("0", 118) + "1", "0." + strings.Repeat("0", 118) + "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOfficialFixture(t)
			f.adapter = "newapi-user-self-v1"
			f.path = "/api/user/self"
			profile := strings.Replace(newAPIConversionProfile, "0.000002", tc.rate, 1)
			newAPIConfigure(f, withNewAPIConversion(newAPISelfContract, profile), 0, "synthetic-query-canary")
			f.response.Store(`{"success":true,"data":{"id":42,"quota":` + tc.quota + `}}`)
			var job officialbalance.Job
			raw := f.wait(f.query())
			json.Unmarshal([]byte(raw), &job)
			if job.Status == nil || job.Status.LastSuccess == nil || job.Status.LastSuccess.AmountDecimal != tc.want || job.Status.LastSuccess.Currency != "CNY" || job.Status.LastSuccess.NativeQuota == nil || *job.Status.LastSuccess.NativeQuota.Remaining != tc.quota {
				t.Fatal("incorrect exact multiplication:", raw)
			}
		})
	}
}

func TestOfficialBalanceNewAPIConversionOverflowAndBadQuotaRetainSuccess(t *testing.T) {
	f := newOfficialFixture(t)
	f.adapter = "newapi-user-self-v1"
	f.path = "/api/user/self"
	profile := strings.Replace(newAPIConversionProfile, "0.000002", strings.Repeat("9", 120), 1)
	newAPIConfigure(f, withNewAPIConversion(newAPISelfContract, profile), 0, "synthetic-query-canary")
	f.response.Store(`{"success":true,"data":{"id":42,"quota":1}}`)
	f.wait(f.query())
	for _, quota := range []string{"10", "null", `"NaN"`, `"Infinity"`, `"nonnumeric"`, "1.5", "1e3"} {
		f.response.Store(`{"success":true,"data":{"id":42,"quota":` + quota + `}}`)
		var job officialbalance.Job
		raw := f.wait(f.query())
		json.Unmarshal([]byte(raw), &job)
		if job.Status == nil || job.Status.State != "parse_error" || !job.Status.Stale || job.Status.LastSuccess == nil || job.Status.LastSuccess.AmountDecimal != strings.Repeat("9", 120) || job.Status.Attempt.AmountDecimal != "" || job.Status.Attempt.Currency != "" {
			t.Fatal("conversion failure replaced success or fabricated money:", raw)
		}
		if quota == "10" && (job.Status.Attempt.ErrorCode != "CONVERSION_PRECISION_EXCEEDED" || job.Status.Attempt.NativeQuota == nil || *job.Status.Attempt.NativeQuota.Remaining != "10") {
			t.Fatal("overflow lost independent native reading:", raw)
		}
	}
}

func TestOfficialBalanceNewAPIConversionTokenScopeAndNonfiniteStates(t *testing.T) {
	f := newOfficialFixture(t)
	f.adapter = "newapi-token-usage-v1"
	f.path = "/api/usage/token/"
	contract := `"newAPI":{"protocolConfirmed":true,"instanceVersion":"fixture-token","nativeUnit":"newapi_quota","auth":"inference-key-readonly-v1"}`
	profile := strings.Replace(strings.Replace(newAPIConversionProfile, "0.000002", "0.25", 1), "CNY", "USD", 1)
	newAPIConfigure(f, withNewAPIConversion(contract, profile), 0, "synthetic-query-canary")
	for _, tc := range []struct{ fields, state, money string }{
		{`"unlimited_quota":false,"total_available":4`, "finite", "1"},
		{`"unlimited_quota":true,"total_available":100000000`, "unlimited", ""},
		{`"total_available":0`, "unknown", ""},
		{`"unlimited_quota":null,"total_available":0`, "unknown", ""},
	} {
		f.response.Store(`{"code":true,"data":{"object":"token_usage","user_quota":{"total_available":999},` + tc.fields + `}}`)
		var job officialbalance.Job
		raw := f.wait(f.query())
		json.Unmarshal([]byte(raw), &job)
		if job.Status == nil || job.Status.LastSuccess == nil {
			t.Fatal(raw)
		}
		obs := job.Status.LastSuccess
		if obs.Semantic != "key_remaining" || obs.NativeQuota.State != tc.state || obs.AmountDecimal != tc.money || tc.money == "" && obs.Currency != "" || len(obs.Balances) != 0 {
			t.Fatal("token allowance became a finite account wallet:", raw)
		}
	}
}

func TestOfficialBalanceNewAPIEntireConversionProfileIsAuthority(t *testing.T) {
	for _, change := range []struct{ name, old, new string }{
		{"proof", "synthetic-operator-proof-1", "synthetic-operator-proof-2"},
		{"contract-version", "synthetic-conversion-v1", "synthetic-conversion-v2"},
		{"currency", "CNY", "USD"},
		{"rate", "0.000002", "0.000003"},
		{"equivalent-rate-lexeme", "0.000002", "0.0000020"},
	} {
		t.Run(change.name, func(t *testing.T) {
			f := newOfficialFixture(t)
			f.adapter = "newapi-user-self-v1"
			f.path = "/api/user/self"
			contract := withNewAPIConversion(newAPISelfContract, newAPIConversionProfile)
			newAPIConfigure(f, contract, 0, "synthetic-query-canary")
			if got := newAPIConfigure(f, contract, 1, ""); !strings.Contains(got, `"configured":true`) {
				t.Fatal("same profile lost PAT:", got)
			}
			changed := strings.Replace(contract, change.old, change.new, 1)
			if got := newAPIConfigure(f, changed, 2, ""); !strings.Contains(got, `"configured":false`) {
				t.Fatal("changed profile inherited old PAT:", got)
			}
			if got := newAPIConfigure(f, changed, 3, "synthetic-query-canary"); !strings.Contains(got, `"configured":true`) {
				t.Fatal("explicit replacement did not establish new authority:", got)
			}
			stale := `{` + balanceBinding + `,"version":3,"adapter":"` + f.adapter + `","endpoint":"` + f.upstream.URL + f.path + `",` + changed + `}`
			testutil.RequireStatus(t, testutil.Request(t, f.handler, "PUT", balancePath, stale, testutil.AdminKey), 409)
			if got := newAPIConfigure(f, newAPISelfContract, 4, ""); !strings.Contains(got, `"configured":false`) {
				t.Fatal("removing profile reused PAT:", got)
			}
		})
	}
}
