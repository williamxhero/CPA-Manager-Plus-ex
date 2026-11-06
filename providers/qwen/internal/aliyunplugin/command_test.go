package aliyunplugin_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"gopkg.in/yaml.v3"
	"cpa-credential-qwen/internal/aliyunplugin"
)

const selectedKey = "selected-credential-fixture-not-for-cli"
const commandReading = `{"source":"bsk","plan":"Token Plan Standard","observedAt":"2026-11-03T12:00:00+08:00","windows":[{"window":"1month","usedPercent":100,"resetTime":"2026-12-01T00:00:00+08:00"},{"window":"5h","usedPercent":12.5}],"metrics":[{"key":"balance_available","label":"Balance","value":0,"unit":"CNY","currency":"CNY","format":"currency"}]}`

func TestDeepRequestSecretsNeverEscapeQuotaErrors(t *testing.T) {
	const secret = "OpaqueReviewCredentialV4+private/7"
	for _, source := range []string{"metadata", "storage_json"} {
		for _, depth := range []int{0, 1, 30, 31, 32, 64} {
			t.Run(fmt.Sprintf("%s/depth-%d", source, depth), func(t *testing.T) {
				var value any = secret
				for i := 0; i < depth; i++ {
					value = map[string]any{"level": value}
				}
				req := pluginapi.QuotaFetchRequest{}
				payload := map[string]any{"payload": value}
				if source == "metadata" {
					req.Metadata = payload
				} else {
					req.StorageJSON, _ = json.Marshal(payload)
				}
				t.Setenv("ALIYUN_COMPAT_STDOUT", `{"error":"not_logged_in","message":"lookup failed `+url.QueryEscape(secret)+`"}`)
				_, env := fetch(t, commandManager(t, "emit", ""), req)
				if env.OK || env.Error == nil || env.Error.Code != "quota_error" || strings.Contains(env.Error.Message, secret) || strings.Contains(env.Error.Message, url.QueryEscape(secret)) {
					t.Fatalf("unsafe quota failure: %#v", env)
				}
			})
		}
	}
}

func TestOverdeepRequestDoesNotStartHelper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper-started")
	t.Setenv("ALIYUN_COMPAT_COUNTFILE", path)
	var value any = "synthetic-secret"
	for i := 0; i < 64; i++ {
		value = []any{value}
	}
	_, env := fetch(t, commandManager(t, "count", ""), pluginapi.QuotaFetchRequest{Metadata: map[string]any{"payload": value}})
	if env.OK || env.Error == nil || !strings.Contains(env.Error.Message, "safe traversal depth") {
		t.Fatalf("overdeep metadata was not rejected: %#v", env)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("helper ran with an incomplete redaction set: %v", err)
	}
}

// The test executable is a portable, shell-free fake CLI. It never calls an
// external service or reads a browser profile, runtime config or credentials.
func TestCommandProcess(t *testing.T) {
	if os.Getenv("ALIYUN_COMPAT_HELPER") != "1" {
		return
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "emit", "fail":
		fmt.Fprint(os.Stdout, os.Getenv("ALIYUN_COMPAT_STDOUT"))
		fmt.Fprint(os.Stderr, os.Getenv("ALIYUN_COMPAT_STDERR"))
		if args[0] == "fail" {
			os.Exit(7)
		}
	case "inspect":
		for _, value := range append(os.Args, os.Environ()...) {
			if strings.Contains(value, selectedKey) {
				os.Exit(9)
			}
		}
		fmt.Fprint(os.Stdout, commandReading)
	case "args":
		var want []string
		wd, err := os.Getwd()
		if json.Unmarshal([]byte(os.Getenv("ALIYUN_COMPAT_ARGS")), &want) != nil || !reflect.DeepEqual(args[1:], want) || err != nil || wd != os.Getenv("ALIYUN_COMPAT_WORKDIR") {
			os.Exit(3)
		}
		fmt.Fprint(os.Stdout, commandReading)
	case "count":
		path := os.Getenv("ALIYUN_COMPAT_COUNTFILE")
		previous, _ := os.ReadFile(path)
		if os.WriteFile(path, append(previous, 'x'), 0600) != nil {
			os.Exit(4)
		}
		fmt.Fprint(os.Stdout, commandReading)
	case "timeout":
		time.Sleep(10 * time.Second)
	case "large-stdout":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 2<<20))
	case "large-stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 32<<10))
	default:
		os.Exit(5)
	}
	os.Exit(0)
}

func commandYAML(t *testing.T, mode string, args ...string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(map[string]any{"command": exe, "command-args": append([]string{"-test.run=^TestCommandProcess$", "--", mode}, args...)})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func commandManager(t *testing.T, mode string, extra string, args ...string) *aliyunplugin.Manager {
	t.Helper()
	t.Setenv("ALIYUN_COMPAT_HELPER", "1")
	m := aliyunplugin.NewManager(nil)
	if env := configure(t, m, commandYAML(t, mode, args...)+extra); !env.OK {
		t.Fatalf("register: %#v", env.Error)
	}
	return m
}

func fetch(t *testing.T, m *aliyunplugin.Manager, req pluginapi.QuotaFetchRequest) (*pluginapi.QuotaFetchResponse, pluginabi.Envelope) {
	t.Helper()
	env := invoke(t, m, pluginabi.MethodQuotaFetch, req)
	if !env.OK {
		return nil, env
	}
	var result pluginapi.QuotaFetchResponse
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	return &result, env
}

func TestCommandCompatibilityFetchMapsRealReadings(t *testing.T) {
	t.Setenv("ALIYUN_COMPAT_STDOUT", commandReading)
	t.Setenv("ALIYUN_COMPAT_STDERR", "diagnostic only, not quota JSON")
	m := commandManager(t, "emit", "")
	result, env := fetch(t, m, pluginapi.QuotaFetchRequest{Provider: "any-provider"})
	if !env.OK {
		t.Fatalf("fetch: %#v", env.Error)
	}
	if result.Subscription == nil || result.Subscription.Plan != "Token Plan Standard" || result.Subscription.TierName != "" || result.ServerTimeOffsetMs != 0 || len(result.Groups) != 1 || result.Groups[0].DisplayName != "阿里云百炼额度（CLI）" || len(result.Groups[0].Buckets) != 2 || len(result.Summary) != 1 {
		t.Fatalf("mapping: %#v", result)
	}
	if result.Groups[0].Buckets[0].RemainingFraction != 0 || result.Groups[0].Buckets[0].ResetTime != "2026-12-01T00:00:00+08:00" || result.Groups[0].Buckets[1].RemainingFraction != .875 || result.Summary[0] != (pluginapi.QuotaMetric{Key: "balance_available", Label: "Balance", Value: 0, Unit: "CNY", Currency: "CNY", Format: "currency"}) {
		t.Fatalf("readings: %#v", result)
	}
}

func TestCommandArgumentsWorkdirAndDisplayNameArePreserved(t *testing.T) {
	args := []string{"--json", "argument with spaces", "\"quoted\"", "& ; | > $(not-a-command)", "中文", "", `C:\path with spaces\file`}
	raw, _ := json.Marshal(args)
	t.Setenv("ALIYUN_COMPAT_ARGS", string(raw))
	wd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALIYUN_COMPAT_WORKDIR", wd)
	extra, _ := yaml.Marshal(map[string]any{"command-workdir": wd, "display-name": "Custom quota"})
	m := commandManager(t, "args", string(extra), args...)
	result, env := fetch(t, m, pluginapi.QuotaFetchRequest{})
	if !env.OK || result.Groups[0].DisplayName != "Custom quota" {
		t.Fatalf("args/workdir: %#v", env.Error)
	}
}

func TestSelectedAPIKeyIsNeverForwardedToCommand(t *testing.T) {
	m := commandManager(t, "inspect", "")
	_, env := fetch(t, m, pluginapi.QuotaFetchRequest{Attributes: map[string]string{"api_key": selectedKey}, Metadata: map[string]any{"api_key": selectedKey}, StorageJSON: []byte(`{"api_key":"` + selectedKey + `"}`)})
	if !env.OK {
		t.Fatalf("credential forwarded or fetch failed: %#v", env.Error)
	}
}

func TestCommandFetchesEveryTimeAndFiltersProviderBeforeExec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls")
	t.Setenv("ALIYUN_COMPAT_COUNTFILE", path)
	m := commandManager(t, "count", "debug-log-requests: false\nsupported-providers: [custom]\n")
	for _, provider := range []string{"other", "CUSTOM", "custom "} {
		_, env := fetch(t, m, pluginapi.QuotaFetchRequest{Provider: provider})
		if env.OK || !strings.Contains(env.Error.Message, "does not support") {
			t.Fatal("provider mismatch was accepted")
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rejected provider ran command")
	}
	for i := 0; i < 2; i++ {
		if _, env := fetch(t, m, pluginapi.QuotaFetchRequest{Provider: "custom"}); !env.OK {
			t.Fatal(env.Error)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "xx" {
		t.Fatalf("fetch was cached: %q, %v", data, err)
	}
}

func TestCommandAmountOnlyWindowsOnlyAndNullOmission(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		buckets, metrics int
		remaining        float64
	}{
		{"amount-only", `{"metrics":[{"key":"balance","value":-3.2}]}`, 0, 1, 0},
		{"windows-only", `{"windows":[{"window":"custom","usedPercent":50}]}`, 1, 0, .5},
		{"missing-and-null-with-real-zero", `{"windows":[{"window":"missing"},{"window":"null","usedPercent":null},{"window":"actual","usedPercent":0}],"metrics":[{"key":"missing"},{"key":"null","value":null},{"key":"actual","value":0}]}`, 1, 1, 1},
		{"omitted-windows-with-valid-reset-times", `{"windows":[{"window":"missing","resetTime":"2026-12-01T00:00:00Z"},{"window":"null","usedPercent":null,"resetTime":"2026-12-01T00:00:00Z"}],"metrics":[{"key":"actual","value":1}]}`, 0, 1, 0},
		{"fully-used", `{"windows":[{"window":"actual","usedPercent":100}]}`, 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ALIYUN_COMPAT_STDOUT", tc.body)
			result, env := fetch(t, commandManager(t, "emit", ""), pluginapi.QuotaFetchRequest{})
			if !env.OK {
				t.Fatal(env.Error)
			}
			count := 0
			for _, group := range result.Groups {
				count += len(group.Buckets)
			}
			if count != tc.buckets || len(result.Summary) != tc.metrics || result.Subscription != nil {
				t.Fatalf("fabricated/missing reading: %#v", result)
			}
			if tc.buckets > 0 && result.Groups[0].Buckets[0].RemainingFraction != tc.remaining {
				t.Fatal("wrong percent")
			}
			if tc.name == "amount-only" && result.Summary[0].Value != -3.2 {
				t.Fatal("amount changed")
			}
			if tc.name == "missing-and-null-with-real-zero" && (result.Summary[0].Key != "actual" || result.Summary[0].Value != 0 || result.Groups[0].Buckets[0].Window != "actual") {
				t.Fatal("null became zero")
			}
		})
	}
}

func TestCommandRejectsInvalidReadingsAndTimes(t *testing.T) {
	bodies := []string{`{}`, `null`, `[]`, `{"plan":"Standard"}`, `{"windows":null,"metrics":null}`, `{"windows":[null],"metrics":[null]}`, `{"windows":[{"window":"x","usedPercent":null}]}`, `{"metrics":[{"key":"null","value":null}]}`, `{"windows":[{"usedPercent":0}]}`, `{"metrics":[{"value":0}]}`, `{"observedAt":"tomorrow","metrics":[{"key":"x","value":1}]}`, `{"windows":[{"window":"x","usedPercent":0,"resetTime":"2026-11-03T00:00:00"}]}`, `{"windows":[{"window":"x","usedPercent":0,"resetTime":"tomorrow"}]}`, commandReading + `{}`}
	for _, percent := range []string{"-0.01", "100.01", "NaN", "Inf", "1e309", `"10"`, "true", "{}", "[]"} {
		bodies = append(bodies, `{"windows":[{"window":"x","usedPercent":`+percent+`}],"metrics":[{"key":"valid","value":1}]}`)
	}
	for _, amount := range []string{"NaN", "Inf", "1e309", `"0"`, "false", "{}", "[]"} {
		bodies = append(bodies, `{"metrics":[{"key":"x","value":`+amount+`}]}`)
	}
	for i, body := range bodies {
		t.Run(fmt.Sprintf("case-%02d", i), func(t *testing.T) {
			t.Setenv("ALIYUN_COMPAT_STDOUT", body)
			result, env := fetch(t, commandManager(t, "emit", ""), pluginapi.QuotaFetchRequest{})
			if result != nil || env.OK || env.Error.Code != "quota_error" {
				t.Fatalf("invalid contract accepted: %s", body)
			}
		})
	}
}

func TestCommandAcceptsRFC3339ObservationAndResetTimes(t *testing.T) {
	for _, value := range []string{"2026-11-03T00:00:00+08:00", "2026-11-03T00:00:00Z", "2026-11-03T00:00:00.123Z"} {
		t.Setenv("ALIYUN_COMPAT_STDOUT", fmt.Sprintf(`{"observedAt":%q,"windows":[{"window":"x","usedPercent":0,"resetTime":%q}]}`, value, value))
		result, env := fetch(t, commandManager(t, "emit", ""), pluginapi.QuotaFetchRequest{})
		if !env.OK || result.Groups[0].Buckets[0].ResetTime != value {
			t.Fatal("RFC3339 changed/rejected")
		}
	}
}

func TestCommandRejectsPresentInvalidResetEvenWhenReadingIsOmitted(t *testing.T) {
	for _, tc := range []struct{ name, window string }{
		{"missing", `{"window":"missing","resetTime":"tomorrow"}`},
		{"null", `{"window":"null","usedPercent":null,"resetTime":"tomorrow"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ALIYUN_COMPAT_STDOUT", `{"windows":[`+tc.window+`],"metrics":[{"key":"actual","value":1}]}`)
			if _, env := fetch(t, commandManager(t, "emit", ""), pluginapi.QuotaFetchRequest{}); env.OK {
				t.Fatal("present invalid resetTime accepted with an omitted reading")
			}
		})
	}
}

func TestCommandErrorsAreBoundedAndSanitizedIncludingZeroExit(t *testing.T) {
	for _, mode := range []string{"emit", "fail"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("ALIYUN_COMPAT_STDOUT", `{"error":"not_logged_in","message":"login required `+selectedKey+` configured-id configured-secret"}`)
			t.Setenv("ALIYUN_COMPAT_STDERR", "must not replace stdout classification")
			m := commandManager(t, mode, "access-key-id: configured-id\naccess-key-secret: configured-secret\n")
			_, env := fetch(t, m, pluginapi.QuotaFetchRequest{Attributes: map[string]string{"api_key": selectedKey}})
			if env.OK || env.Error.Code != "quota_error" || !strings.Contains(env.Error.Message, "not_logged_in") || !strings.Contains(env.Error.Message, "login required") {
				t.Fatalf("diagnostic lost: %#v", env.Error)
			}
			for _, secret := range []string{selectedKey, "configured-id", "configured-secret", "must not replace"} {
				if strings.Contains(env.Error.Message, secret) {
					t.Fatal("secret or unrelated stderr leaked")
				}
			}
		})
	}
	for _, tc := range []struct{ stdout, stderr string }{{"", selectedKey}, {"not JSON " + selectedKey, ""}, {"{broken " + selectedKey, ""}, {"", strings.Repeat("x", 8000) + selectedKey}} {
		t.Setenv("ALIYUN_COMPAT_STDOUT", tc.stdout)
		t.Setenv("ALIYUN_COMPAT_STDERR", tc.stderr)
		_, env := fetch(t, commandManager(t, "emit", ""), pluginapi.QuotaFetchRequest{Metadata: map[string]any{"nested": map[string]any{"key": selectedKey}}})
		if env.OK || strings.Contains(env.Error.Message, selectedKey) || len(env.Error.Message) > 4200 {
			t.Fatalf("unsafe error: %#v", env.Error)
		}
	}
}

func TestCommandOutputLimitsTimeoutAndStartFailures(t *testing.T) {
	for _, mode := range []string{"large-stdout", "large-stderr", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			start := time.Now()
			extra := "command-timeout: 5s\n"
			if mode == "timeout" {
				extra = "command-timeout: 150ms\n"
			}
			_, env := fetch(t, commandManager(t, mode, extra), pluginapi.QuotaFetchRequest{})
			if env.OK || time.Since(start) > 4*time.Second || len(env.Error.Message) > 4200 {
				t.Fatalf("unbounded subprocess: %#v", env.Error)
			}
		})
	}
	for _, overrides := range []map[string]any{{"command": filepath.Join(t.TempDir(), "missing-executable")}, {"command-workdir": filepath.Join(t.TempDir(), "missing-dir")}} {
		base := map[string]any{}
		if err := yaml.Unmarshal([]byte(commandYAML(t, "emit")), &base); err != nil {
			t.Fatal(err)
		}
		for k, v := range overrides {
			base[k] = v
		}
		data, _ := yaml.Marshal(base)
		m := aliyunplugin.NewManager(nil)
		if env := configure(t, m, string(data)); !env.OK {
			t.Fatal(env.Error)
		}
		if _, env := fetch(t, m, pluginapi.QuotaFetchRequest{}); env.OK {
			t.Fatal("start failure accepted")
		}
	}
}
