package bailianquota

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Public fixture: modelstudioai/cli@a7cb810c68f434c2c7330d8c0775614f7e699c8d,
// packages/commands/tests/coding-plan-usage.test.ts:68-78. No account data.
const codingQuotaFixture = `{"codingPlanInstanceInfos":[{"status":"EXPIRED","instanceType":"ignored"},{"status":"VALID","instanceType":"pro","codingPlanQuotaInfo":{"per5HourUsedQuota":38,"per5HourTotalQuota":100,"per5HourQuotaNextRefreshTime":1786000000000,"perWeekUsedQuota":500,"perWeekTotalQuota":1000,"perWeekQuotaNextRefreshTime":1786100000000,"perBillMonthUsedQuota":950,"perBillMonthTotalQuota":1000,"perBillMonthQuotaNextRefreshTime":1786200000000}}]}`

type codingTransport func(*http.Request) (*http.Response, error)

func (f codingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func codingResponse(data string) string {
	return `{"code":"200","successResponse":true,"data":{"success":true,"DataV2":{"success":true,"data":{"success":true,"code":"SUCCESS","data":` + data + `}}}}`
}

func codingCLIArgs(t *testing.T) ([]string, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "explicit-console-token.txt")
	if err := os.WriteFile(file, []byte("synthetic-console-token"), 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"--protocol", "qwen-coding-plan-v1", "--source", "coding-plan", "--token-file", file, "--console-region", "cn-beijing", "--console-site", "domestic"}, file
}

func useCodingTransport(t *testing.T, transport codingTransport) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func TestCodingPlanCLIReadOnlyThreeWindows(t *testing.T) {
	args, tokenFile := codingCLIArgs(t)
	calls := 0
	useCodingTransport(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Scheme != "https" || r.URL.Host != "bailian-cs.console.aliyun.com" || r.URL.Path != "/cli/api.json" || r.URL.Query().Get("action") != "BroadScopeAspnGateway" || r.URL.Query().Get("product") != "sfm_bailian" || r.URL.Query().Get("api") != "zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2" {
			t.Errorf("wrong read-only Coding Plan target: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-console-token" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Cookie") != "" {
			t.Error("missing explicit console token or unexpected cookie")
		}
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		var params map[string]any
		if err := json.Unmarshal([]byte(form.Get("params")), &params); err != nil {
			t.Fatal(err)
		}
		var want map[string]any
		_ = json.Unmarshal([]byte(`{"Api":"zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2","V":"1.0","Data":{"queryCodingPlanInstanceInfoRequest":{"commodityCode":"sfm_codingplan_public_cn","onlyLatestOne":true},"cornerstoneParam":{"protocol":"V2","console":"ONE_CONSOLE","productCode":"p_efm","switchUserType":3,"consoleSite":"BAILIAN_ALIYUN"}}}`), &want)
		if !reflect.DeepEqual(params, want) || form.Get("region") != "cn-beijing" || len(form) != 2 {
			t.Errorf("wrong Coding Plan request: %s", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(codingResponse(codingQuotaFixture)))}, nil
	})
	var out, diagnostics bytes.Buffer
	exit := RunCLI(context.Background(), args, &out, &diagnostics)
	if exit != 0 || calls != 1 {
		t.Fatalf("want one read-only Coding Plan query, exit=%d calls=%d output=%s", exit, calls, out.String())
	}
	var result struct {
		Protocol, Product, QueryStatus, AuthorityStatus, Reason, ObservedAt, InstanceType string
		Windows                                                                           []struct{ Window, Unit, Used, Total, Remaining, ResetTime string }
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Protocol != "qwen-coding-plan-v1" || result.Product != "coding-plan" || result.QueryStatus != "success" || result.AuthorityStatus != "needs_configuration" || result.Reason != "account_authority_unverified" || result.ObservedAt == "" || result.InstanceType != "pro" {
		t.Fatalf("query success must not claim selected-key authority: %s", out.String())
	}
	if len(result.Windows) != 3 || result.Windows[0].Window != "5h" || result.Windows[0].Unit != "requests" || result.Windows[0].Used != "38" || result.Windows[0].Total != "100" || result.Windows[0].Remaining != "62" || result.Windows[0].ResetTime != "2026-08-06T07:06:40Z" || result.Windows[1].Remaining != "500" || result.Windows[2].Remaining != "50" {
		t.Fatalf("wrong public three-window vector: %s", out.String())
	}
	unchanged, _ := os.ReadFile(tokenFile)
	if string(unchanged) != "synthetic-console-token" || strings.Contains(out.String()+diagnostics.String(), "synthetic-console-token") {
		t.Fatal("explicit token was changed or exposed")
	}
}

func TestCodingPlanCLIRejectsNestedErrorsBeforeQuota(t *testing.T) {
	valid := codingResponse(codingQuotaFixture)
	for name, body := range map[string]string{
		"outer_false":    strings.Replace(valid, `"data":{"success":true`, `"data":{"success":false`, 1),
		"root_error":     strings.Replace(valid, `"code":"200"`, `"error":{"code":"Denied"},"code":"200"`, 1),
		"business_error": strings.Replace(valid, `"code":"SUCCESS"`, `"error":{"code":"Denied"},"code":"SUCCESS"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			args, _ := codingCLIArgs(t)
			useCodingTransport(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			var out bytes.Buffer
			if exit := RunCLI(context.Background(), args, &out, io.Discard); exit == 0 || !strings.Contains(out.String(), `"error":"rpc_error"`) || strings.Contains(out.String(), `"windows"`) {
				t.Fatalf("nested failure became a reading: exit=%d output=%s", exit, out.String())
			}
		})
	}
}

func TestCodingPlanCLIRejectsIncompleteOrUnsafeReadings(t *testing.T) {
	for name, body := range map[string]string{
		"missing":           codingResponse(strings.Replace(codingQuotaFixture, `"per5HourUsedQuota":38,`, "", 1)),
		"null":              codingResponse(strings.Replace(codingQuotaFixture, `"per5HourUsedQuota":38`, `"per5HourUsedQuota":null`, 1)),
		"numeric_string":    codingResponse(strings.Replace(codingQuotaFixture, `"per5HourUsedQuota":38`, `"per5HourUsedQuota":"38"`, 1)),
		"fraction":          codingResponse(strings.Replace(codingQuotaFixture, `"per5HourUsedQuota":38`, `"per5HourUsedQuota":38.5`, 1)),
		"exponent":          codingResponse(strings.Replace(codingQuotaFixture, `"per5HourUsedQuota":38`, `"per5HourUsedQuota":1e999`, 1)),
		"negative":          codingResponse(strings.Replace(codingQuotaFixture, `"per5HourUsedQuota":38`, `"per5HourUsedQuota":-1`, 1)),
		"overrun":           codingResponse(strings.Replace(codingQuotaFixture, `"per5HourUsedQuota":38`, `"per5HourUsedQuota":101`, 1)),
		"zero_total":        codingResponse(strings.Replace(codingQuotaFixture, `"per5HourTotalQuota":100`, `"per5HourTotalQuota":0`, 1)),
		"seconds":           codingResponse(strings.Replace(codingQuotaFixture, "1786000000000", "1786000000", 1)),
		"fractional_millis": codingResponse(strings.Replace(codingQuotaFixture, "1786000000000", "1786000000000.5", 1)),
		"no_valid_instance": codingResponse(`{"codingPlanInstanceInfos":[]}`),
		"login_json":        `{"success":true,"data":{"accountId":"synthetic-account"}}`,
		"trailing_json":     codingResponse(codingQuotaFixture) + ` {"success":true}`,
		"nested_login":      strings.Replace(codingResponse(codingQuotaFixture), `"code":"SUCCESS"`, `"code":"ConsoleNeedLogin"`, 1),
		"oversized":         strings.Repeat(" ", maxResponseBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			args, _ := codingCLIArgs(t)
			calls := 0
			useCodingTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			var out bytes.Buffer
			if exit := RunCLI(context.Background(), args, &out, io.Discard); exit == 0 || calls != 1 || strings.Contains(out.String(), `"windows"`) || !strings.Contains(out.String(), `"error"`) {
				t.Fatalf("invalid input became a reading or triggered fallback: exit=%d calls=%d output=%s", exit, calls, out.String())
			}
		})
	}
}

func TestCodingPlanCLIExactCountsAndMilliseconds(t *testing.T) {
	args, _ := codingCLIArgs(t)
	body := strings.Replace(codingQuotaFixture, `"per5HourUsedQuota":38,"per5HourTotalQuota":100`, `"per5HourUsedQuota":9007199254740993,"per5HourTotalQuota":9007199254740995`, 1)
	body = strings.Replace(body, "1786000000000", "1786000000123", 1)
	useCodingTransport(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(codingResponse(body)))}, nil
	})
	var out bytes.Buffer
	if exit := RunCLI(context.Background(), args, &out, io.Discard); exit != 0 {
		t.Fatal(out.String())
	}
	// Independent integers deliberately exceed JS/float64's safe range.
	for _, want := range []string{`"unit":"requests"`, `"used":"9007199254740993"`, `"total":"9007199254740995"`, `"remaining":"2"`, `"resetTime":"2026-08-06T07:06:40.123Z"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("lost exact count or millisecond precision (%s): %s", want, out.String())
		}
	}
}

func TestCodingPlanCLINoLegacySourceOrImplicitEnrollment(t *testing.T) {
	// Strict Coding Plan cache is now explicit opt-in. Cache-only without a
	// file stays invalid; legacy sources, enrollment and mixed transports do not.
	for _, extra := range [][]string{{"--source", "bl"}, {"--source", "cookie"}, {"--browser", "default"}, {"--cache-only"}, {"--check"}, {"--bl-path", "must-not-run"}, {"--console-site", "unknown"}} {
		t.Run(strings.Join(extra, "_"), func(t *testing.T) {
			args, tokenFile := codingCLIArgs(t)
			calls := 0
			useCodingTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				t.Error("legacy or invalid target reached network")
				return nil, io.EOF
			})
			var out bytes.Buffer
			if exit := RunCLI(context.Background(), append(args, extra...), &out, io.Discard); exit == 0 || calls != 0 || !strings.Contains(out.String(), "needs_configuration") {
				t.Fatalf("legacy or invalid target was accepted: exit=%d calls=%d output=%s", exit, calls, out.String())
			}
			unchanged, _ := os.ReadFile(tokenFile)
			if string(unchanged) != "synthetic-console-token" {
				t.Fatal("query modified pre-enrolled token")
			}
		})
	}
}

func TestCodingPlanCLIInternationalAndExpiredTokenNoRefresh(t *testing.T) {
	for _, status := range []int{200, 302, 401, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			args, _ := codingCLIArgs(t)
			args = append(args, "--console-region", "ap-southeast-1", "--console-site", "international")
			calls := 0
			useCodingTransport(t, func(r *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(r.Body)
				if r.URL.Host != "bailian-singapore-cs.alibabacloud.com" || r.URL.Query().Get("action") != "IntlBroadScopeAspnGateway" || !strings.Contains(string(body), "sfm_codingplan_public_intl") || !strings.Contains(string(body), "ap-southeast-1") {
					t.Error("wrong explicit international target")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://must-not-follow.invalid/login"}}, Body: io.NopCloser(strings.NewReader(codingResponse(codingQuotaFixture)))}, nil
			})
			var out bytes.Buffer
			exit := RunCLI(context.Background(), args, &out, io.Discard)
			if calls != 1 || (status == 200) != (exit == 0) || status != 200 && !strings.Contains(out.String(), `"error":"not_logged_in"`) {
				t.Fatalf("query followed redirect/refreshed/logged in or hid failure: exit=%d calls=%d output=%s", exit, calls, out.String())
			}
		})
	}
}

func TestCodingPlanCLILoginJSONIsNotTokenOrOwnership(t *testing.T) {
	args, tokenFile := codingCLIArgs(t)
	login := `{"success":true,"accountId":"user-entered-account","token":"synthetic-console-token","verified":true}`
	if err := os.WriteFile(tokenFile, []byte(login), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	useCodingTransport(t, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, io.EOF
	})
	var out bytes.Buffer
	if exit := RunCLI(context.Background(), args, &out, io.Discard); exit == 0 || calls != 0 || !strings.Contains(out.String(), "needs_configuration") || strings.Contains(out.String(), "synthetic-console-token") {
		t.Fatalf("ordinary login JSON triggered query/enrollment or became ownership: exit=%d calls=%d output=%s", exit, calls, out.String())
	}
	unchanged, _ := os.ReadFile(tokenFile)
	if string(unchanged) != login {
		t.Fatal("query rewrote enrollment JSON")
	}
}
