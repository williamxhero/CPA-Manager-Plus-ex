// Public-seam BSS compatibility regressions migrated from CPA-Aliyun-Quota.
package aliyunplugin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"cpa-credential-qwen/internal/aliyunplugin"
)

func mockManager(t *testing.T, handler http.HandlerFunc, extra string, bridge *aliyunplugin.HostBridge) *aliyunplugin.Manager {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	m := aliyunplugin.NewManager(bridge)
	// Tests construct synthetic markers, not actual credentials or committed config.
	if !strings.Contains(extra, "include-bill:") {
		extra += "include-bill: false\n"
	}
	yaml := fmt.Sprintf("quota-source: bss\nendpoint: %s\nallow-http: true\naccess-key-id: signature-test-id\naccess-key-secret: signature-test-value\n%s", server.URL, extra)
	if env := configure(t, m, yaml); !env.OK {
		t.Fatalf("configure: %#v", env.Error)
	}
	return m
}

func TestDescribeConfiguredProvidersNoReset(t *testing.T) {
	m := aliyunplugin.NewManager(nil)
	configure(t, m, "quota-source: bss\nsupported-providers: [custom]")
	env := invoke(t, m, pluginabi.MethodQuotaDescribe, map[string]any{})
	var desc pluginapi.QuotaDescribeResponse
	json.Unmarshal(env.Result, &desc)
	if !env.OK || desc.SupportsReset || desc.DisplayName != "阿里云余额（AK/SK）" || len(desc.SupportedProviders) != 1 || desc.SupportedProviders[0] != "custom" {
		t.Fatal("describe mismatch")
	}
}

func TestQuotaResetUnsupported(t *testing.T) {
	env := invoke(t, aliyunplugin.NewManager(nil), pluginabi.MethodQuotaReset, map[string]any{})
	if env.OK || env.Error.Code != "not_supported" {
		t.Fatal("reset pretends success")
	}
}

func TestMalformedRequests(t *testing.T) {
	for _, method := range []string{pluginabi.MethodPluginRegister, pluginabi.MethodQuotaFetch} {
		raw, err := aliyunplugin.NewManager(nil).HandleCall(method, []byte("{"))
		if err != nil {
			t.Fatal(err)
		}
		var env pluginabi.Envelope
		json.Unmarshal(raw, &env)
		if env.OK || env.Error.Code != "invalid_request" {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestFetchMoneyNoFabricatedWindows(t *testing.T) {
	m := mockManager(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"AvailableAmount":"12.34","CreditAmount":"0.00","Currency":"CNY"}}`)
	}, "", nil)
	result, err := m.FetchQuota(pluginapi.QuotaFetchRequest{Provider: "any-provider"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Summary) != 2 || result.Summary[0].Key != "balance_available" || result.Summary[0].Value != 12.34 || result.Summary[0].Format != "currency" || result.Summary[0].Unit != "CNY" || result.Summary[1].Value != 0 || len(result.Groups) != 1 || result.Groups[0].DisplayName != "阿里云账户" || len(result.Groups[0].Buckets) != 0 || result.Subscription != nil {
		t.Fatalf("unexpected quotas: %#v", result)
	}
}

func TestFetchMissingAmountsNeverCreatesZero(t *testing.T) {
	m := mockManager(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"Currency":"CNY"}}`)
	}, "", nil)
	result, err := m.FetchQuota(pluginapi.QuotaFetchRequest{})
	if err == nil && len(result.Summary) != 0 {
		t.Fatal("fabricated amount")
	}
}

func TestBalanceErrorPropagates(t *testing.T) {
	m := mockManager(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"Success":false,"Code":"InvalidAccessKeyId.NotFound","Message":"key not found"}`)
	}, "", nil)
	env := invoke(t, m, pluginabi.MethodQuotaFetch, pluginapi.QuotaFetchRequest{Provider: "custom"})
	if env.OK || !strings.Contains(env.Error.Message, "InvalidAccessKeyId.NotFound") || strings.Contains(env.Error.Message, "signature-test-value") {
		t.Fatalf("incorrect error: %#v", env.Error)
	}
}

func TestOptionalFailuresPreserveBalance(t *testing.T) {
	m := mockManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Action") == "QueryAccountBalance" {
			fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"AvailableAmount":"9"}}`)
		} else {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"Success":false,"Code":"Denied","Message":"not authorized"}`)
		}
	}, "include-bill: true\ninclude-resource-packages: true\n", nil)
	result, err := m.FetchQuota(pluginapi.QuotaFetchRequest{})
	if err != nil || len(result.Summary) != 1 || result.Summary[0].Value != 9 || len(result.Groups[0].Buckets) != 0 {
		t.Fatalf("optional failure breaks balance: %v", err)
	}
}

func TestOptionalBillAndPackagesMapping(t *testing.T) {
	m := mockManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("Action") {
		case "QueryAccountBalance":
			fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"AvailableCashAmount":"5","Currency":"CNY"}}`)
		case "QueryAccountBill":
			if r.URL.Query().Get("Granularity") != "MONTHLY" || r.URL.Query().Get("IsGroupByProduct") != "true" || len(r.URL.Query().Get("BillingCycle")) != 7 {
				t.Error("incorrect bill parameters")
			}
			fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"Items":{"Item":[{"PretaxAmount":"3.5"},{"PaymentAmount":"1.5"}]}}}`)
		case "QueryResourcePackageInstances":
			fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"Instances":{"Instance":[{"InstanceId":"p1","PackageName":"quota-pack","ProductCode":"bailian","RemainingAmount":"25","TotalAmount":"100","ExpiryTime":"2027-01-01T00:00:00Z"}]}}}`)
		}
	}, "include-bill: true\ninclude-resource-packages: true\n", nil)
	result, err := m.FetchQuota(pluginapi.QuotaFetchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Summary) != 2 || result.Summary[1].Key != "month_spend" || result.Summary[1].Value != 5 || len(result.Groups[0].Buckets) != 1 || result.Groups[0].Buckets[0].Window != "resource_package:quota-pack" || result.Groups[0].Buckets[0].RemainingFraction != .25 || result.Groups[0].Buckets[0].ResetTime != "2027-01-01T00:00:00Z" {
		t.Fatalf("mapping: %#v", result)
	}
}

func TestDebugDisabledFiltersProviderBeforeHTTP(t *testing.T) {
	m := mockManager(t, func(w http.ResponseWriter, r *http.Request) { t.Error("must not call mock") }, "debug-log-requests: false\n", nil)
	if _, err := m.FetchQuota(pluginapi.QuotaFetchRequest{Provider: "other"}); err == nil {
		t.Fatal("unexpected provider accepted")
	}
}
