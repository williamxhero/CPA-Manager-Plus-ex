package bailianquota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Exercise the existing CLI seam, substituting only external commands/HTTP and
// the clock. Any accidental source fallback fails rather than reaching a service.
func absorptionClient(t *testing.T) *Client {
	t.Helper()
	c := testClient()
	c.Run = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("unexpected browser command or fallback")
		return nil, nil
	}
	c.RunBL = func(context.Context, string, ...string) ([]byte, []byte, error) {
		t.Fatal("unexpected official CLI command or fallback")
		return nil, nil, nil
	}
	c.HTTP = doerFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP request or fallback")
		return nil, nil
	})
	return c
}

func absorptionCLI(t *testing.T, c *Client, args ...string) (int, []byte) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), args, &stdout, &stderr, c)
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var object map[string]json.RawMessage
	if decoder.Decode(&object) != nil || object == nil {
		t.Fatalf("exit=%d invalid JSON: %s", code, stdout.Bytes())
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		t.Fatalf("more than one JSON value: %s", stdout.Bytes())
	}
	if strings.Contains(stdout.String()+stderr.String(), "private-secret") {
		t.Fatal("external diagnostics/credentials leaked")
	}
	return code, stdout.Bytes()
}

func absorptionBrowser(t *testing.T, c *Client, list, want string, payload []byte) {
	t.Helper()
	stopped := false
	c.Run = func(ctx context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "browsers":
			return []byte(list), nil
		case "session":
			if args[1] == "start" {
				if !reflect.DeepEqual(args, []string{"session", "start", "--json", "--browser", want, "--no-focus"}) {
					t.Fatalf("wrong/focused browser: %v", args)
				}
				return []byte(`{"session_id":"q8-owned"}`), nil
			}
			if !reflect.DeepEqual(args, []string{"session", "stop", "q8-owned"}) || ctx.Err() != nil {
				t.Fatal("unsafe session cleanup", args, ctx.Err())
			}
			stopped = true
			return nil, nil
		case "navigate":
			return nil, nil
		case "evaluate":
			return payload, nil
		default:
			t.Fatalf("unexpected browser command: %v", args)
			return nil, nil
		}
	}
	t.Cleanup(func() {
		if !stopped {
			t.Error("invocation's session was not stopped")
		}
	})
}

func TestQ8CLIDefaultBrowserSkipsUnresponsiveEntries(t *testing.T) {
	c := absorptionClient(t)
	absorptionBrowser(t, c, `[{"instance_id":"stale","unresponsive":true},{"instance_id":""},{"instance_id":"responsive"}]`, "responsive", browserPayload())
	code, body := absorptionCLI(t, c, "--pretty", "--verbose")
	var result Result
	if json.Unmarshal(body, &result) != nil || code != 0 || result.Source != "bsk" || len(result.Windows) != 1 || result.Windows[0].UsedPercent != 100 {
		t.Fatalf("default browser lost usable reading: exit=%d %s", code, body)
	}
}

func TestQ8CLIResetCardsRemainOptionalTokenPlanMetrics(t *testing.T) {
	for _, source := range []string{"bsk", "cookie"} {
		for _, tc := range []struct {
			name, cards string
			want        float64
		}{{"zero", `[]`, 0}, {"two", `[{"id":"private-secret"},{"id":"another"}]`, 2}} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				c := absorptionClient(t)
				input := map[string]json.RawMessage{
					"usage":        fixture(`{"per1MonthPercentage":0}`),
					"subscription": fixture(`{"specCode":"standard","status":"VALID"}`),
					"addon":        fixture(`{"remainingCredits":0,"totalCredits":0}`),
					"resetCards":   fixture(tc.cards),
					"coding":       fixture(`{"codingPlanInstanceInfos":[{"status":"INVALID","instanceName":"Expired private-secret"}],"userId":"private-secret"}`),
				}
				args := []string{"--source", source}
				if source == "bsk" {
					payload, _ := json.Marshal(input)
					absorptionBrowser(t, c, `[{"instance_id":"responsive"}]`, "responsive", payload)
					run := c.Run
					c.Run = func(ctx context.Context, args ...string) ([]byte, error) {
						if args[0] == "evaluate" && !strings.Contains(args[1], url.QueryEscape("zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/reset-card/list")) {
							t.Error("browser query omitted reset-card API")
						}
						return run(ctx, args...)
					}
				} else {
					args = append(args, "--cookie", "session=private-secret")
					c.HTTP = doerFunc(func(req *http.Request) (*http.Response, error) {
						for _, endpoint := range []struct{ suffix, name string }{
							{"-usage", "usage"}, {"-subscription", "subscription"}, {"-addon-summary", "addon"},
							{"-reset-card-list", "resetCards"}, {"-queryCodingPlanInstanceInfoV2", "coding"},
						} {
							if strings.HasSuffix(req.URL.Query().Get("api"), endpoint.suffix) {
								return httpResponse(200, string(input[endpoint.name])), nil
							}
						}
						t.Fatal("unexpected product API", req.URL)
						return nil, nil
					})
				}
				code, body := absorptionCLI(t, c, args...)
				var result Result
				if json.Unmarshal(body, &result) != nil || code != 0 || len(result.Windows) != 1 || result.Windows[0].Window != "1month" || result.Windows[0].UsedPercent != 0 || len(result.Metrics) != 3 {
					t.Fatalf("old genuine zero/reset-card capability lost: exit=%d %s", code, body)
				}
				if result.Metrics[0].Key != "addon_remaining_credits" || result.Metrics[0].Value != 0 || result.Metrics[1].Key != "addon_total_credits" || result.Metrics[1].Value != 0 || result.Metrics[2].Key != "reset_cards" || result.Metrics[2].Value != tc.want || result.Metrics[2].Unit != "count" || result.Metrics[2].Format != "number" {
					t.Fatalf("incorrect Token Plan metrics: %s", body)
				}
			})
		}
	}
}

// A valid numeric payload is deliberately retained inside each failed wrapper:
// rejecting an otherwise empty/malformed response does not prove this contract.
func absorptionEnvelope(t *testing.T, layer, key string, value any) string {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(realUsageFixture), &root); err != nil {
		t.Fatal(err)
	}
	gateway := root["data"].(map[string]any)
	v2 := gateway["DataV2"].(map[string]any)
	v2["success"] = true
	business := v2["data"].(map[string]any)
	node := map[string]map[string]any{"root": root, "gateway": gateway, "v2": v2, "business": business}[layer]
	node[key] = value
	body, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func absorptionPrimaryOnly(t *testing.T, c *Client, body string) {
	t.Helper()
	c.HTTP = doerFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Query().Get("api"), "-usage") {
			t.Fatal("failed primary query reached optional enrichment", req.URL)
		}
		return httpResponse(200, body), nil
	})
}

func TestQ8CLIEnvelopeFailuresOverrideNumericUsage(t *testing.T) {
	for _, tc := range []struct {
		layer, key, name string
		value            any
	}{
		{"root", "successResponse", "false", false}, {"root", "successResponse", "null", nil}, {"root", "successResponse", "string", "true"},
		{"root", "httpStatusCode", "failure", 500}, {"root", "httpStatusCode", "null", nil},
		{"gateway", "success", "false", false}, {"gateway", "success", "null", nil}, {"gateway", "success", "string", "true"},
		{"gateway", "httpStatus", "failure", 403}, {"gateway", "httpStatus", "null", nil},
		{"v2", "success", "false", false}, {"v2", "success", "null", nil}, {"v2", "success", "string", "true"},
		{"v2", "httpStatus", "failure", 503}, {"v2", "httpStatus", "null", nil},
		{"v2", "ret", "failure", []string{"InvalidApi::private-secret"}}, {"v2", "ret", "empty", []string{}},
		{"v2", "ret", "null", nil}, {"v2", "ret", "string", "SUCCESS"}, {"v2", "ret", "mixed", []any{"SUCCESS::OK", false}},
		{"business", "success", "false", false}, {"business", "success", "null", nil}, {"business", "success", "string", "true"},
		{"business", "failed", "true", true}, {"business", "failed", "null", nil}, {"business", "failed", "string", "false"},
	} {
		t.Run(tc.layer+"/"+tc.key+"/"+tc.name, func(t *testing.T) {
			c := absorptionClient(t)
			absorptionPrimaryOnly(t, c, absorptionEnvelope(t, tc.layer, tc.key, tc.value))
			code, body := absorptionCLI(t, c, "--source", "cookie", "--cookie", "session=private-secret", "--verbose")
			if code != 1 {
				t.Fatalf("failed envelope became a successful reading: exit=%d %s", code, body)
			}
			assertBLFailure(t, body, "rpc_error")
		})
	}
}

func TestQ8CLIExpiredConsoleSessionPreservesLoginClassification(t *testing.T) {
	for _, source := range []string{"bsk", "cookie"} {
		for _, code := range []string{"FAIL_SYS_SESSION_EXPIRED", "FAIL_SYS_TOKEN_EXOIRED", "FAIL_SYS_TOKEN_EXPIRED"} {
			for _, layer := range []string{"root", "gateway", "v2"} {
				for _, mode := range []string{"query", "check"} {
					t.Run(source+"/"+code+"/"+layer+"/"+mode, func(t *testing.T) {
						key, value := "code", any(code)
						if layer == "gateway" {
							key = "errorCode"
						} else if layer == "v2" {
							key, value = "ret", []string{code + "::private-secret"}
						}
						body := absorptionEnvelope(t, layer, key, value)
						c := absorptionClient(t)
						args := []string{"--source", source}
						if source == "cookie" {
							args = append(args, "--cookie", "session=private-secret")
							absorptionPrimaryOnly(t, c, body)
						} else {
							payload, _ := json.Marshal(map[string]json.RawMessage{"usage": json.RawMessage(body)})
							absorptionBrowser(t, c, `[{"instance_id":"responsive"}]`, "responsive", payload)
						}
						if mode == "check" {
							args = append(args, "--check")
						}
						exit, stdout := absorptionCLI(t, c, args...)
						if exit != 1 {
							t.Fatalf("expired login became success: %d %s", exit, stdout)
						}
						assertBLFailure(t, stdout, "not_logged_in")
						if !bytes.Contains(stdout, []byte("log in manually")) {
							t.Fatalf("lost manual renewal hint: %s", stdout)
						}
					})
				}
			}
		}
	}
}

func TestQ8CLIBrowserAccountNoteDoesNotClaimKeyOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, account string
		wantNote      bool
	}{{"primary_account", `"主账号"`, true}, {"untrusted_text", `"private-secret"`, false}, {"null", `null`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			c := absorptionClient(t)
			input := responses()
			input["account"] = json.RawMessage(tc.account)
			payload, _ := json.Marshal(input)
			absorptionBrowser(t, c, `[{"instance_id":"responsive"}]`, "responsive", payload)
			run := c.Run
			c.Run = func(ctx context.Context, args ...string) ([]byte, error) {
				if args[0] == "evaluate" && !strings.Contains(args[1], `out.account="主账号"`) {
					t.Error("browser no longer reports the rendered primary-account label")
				}
				return run(ctx, args...)
			}
			code, body := absorptionCLI(t, c)
			var result Result
			if json.Unmarshal(body, &result) != nil || code != 0 || len(result.Windows) != 1 {
				t.Fatalf("account note affected the reading: %d %s", code, body)
			}
			notes := strings.Join(result.Notes, " ")
			if strings.Contains(notes, "控制台账户类型：主账号") != tc.wantNote || tc.wantNote && !strings.Contains(notes, "不是 API Key 归属证明") {
				t.Fatalf("lost safe account-type note: %s", body)
			}
		})
	}
}

func TestQ8CLINumericMissingNullAndRealZero(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       float64
		valid      bool
	}{
		{"zero", `{"per1MonthPercentage":0}`, 0, true},
		{"fraction", `{"per1MonthPercentage":0.125,"per1MonthResetTime":null}`, 12.5, true},
		{"full", `{"per1MonthPercentage":1}`, 100, true},
		{"missing", `{}`, 0, false}, {"null", `{"per1MonthPercentage":null}`, 0, false},
		{"empty_string", `{"per1MonthPercentage":""}`, 0, false}, {"numeric_string", `{"per1MonthPercentage":"0.5"}`, 0, false},
		{"bool", `{"per1MonthPercentage":true}`, 0, false}, {"object", `{"per1MonthPercentage":{}}`, 0, false},
		{"array", `{"per1MonthPercentage":[]}`, 0, false}, {"negative", `{"per1MonthPercentage":-0.1}`, 0, false},
		{"over", `{"per1MonthPercentage":1.01}`, 0, false}, {"overflow", `{"per1MonthPercentage":1e309}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := absorptionClient(t)
			c.HTTP = doerFunc(func(req *http.Request) (*http.Response, error) {
				if strings.HasSuffix(req.URL.Query().Get("api"), "-usage") {
					return httpResponse(200, string(fixture(tc.data))), nil
				}
				if !tc.valid {
					t.Fatal("invalid primary queried optional APIs")
				}
				return httpResponse(500, "private-secret"), nil
			})
			code, body := absorptionCLI(t, c, "--source", "cookie", "--cookie", "session=private-secret")
			if !tc.valid {
				if code != 1 {
					t.Fatalf("invalid quota became success: %s", body)
				}
				assertBLFailure(t, body, "invalid_quota")
				return
			}
			var result Result
			if json.Unmarshal(body, &result) != nil || code != 0 || len(result.Windows) != 1 || result.Windows[0].UsedPercent != tc.want || result.Windows[0].ResetTime != "" || len(result.Metrics) != 0 || len(result.Notes) != 4 || result.ObservedAt != "2026-10-08T17:30:00+08:00" {
				t.Fatalf("real zero/fraction/missing reset changed: %d %s", code, body)
			}
		})
	}
}

func TestQ8CLIResetCardFailuresNeverInventZero(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"null", string(fixture(`null`))}, {"object", string(fixture(`{}`))}, {"string", string(fixture(`"[]"`))},
		{"invalid_json", `not JSON private-secret`}, {"expired", `{"code":"ConsoleNeedLogin","message":"private-secret"}`},
		{"failed_envelope", `{"code":"200","successResponse":false,"data":{"success":true,"DataV2":{"data":{"success":true,"data":[]}}}}`},
		{"unavailable", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := absorptionClient(t)
			c.HTTP = doerFunc(func(req *http.Request) (*http.Response, error) {
				if strings.HasSuffix(req.URL.Query().Get("api"), "-usage") {
					return httpResponse(200, realUsageFixture), nil
				}
				if strings.HasSuffix(req.URL.Query().Get("api"), "-reset-card-list") && tc.body != "" {
					return httpResponse(200, tc.body), nil
				}
				return nil, errors.New("private-secret")
			})
			code, body := absorptionCLI(t, c, "--source", "cookie", "--cookie", "session=private-secret")
			var result Result
			if json.Unmarshal(body, &result) != nil || code != 0 || len(result.Windows) != 1 || len(result.Metrics) != 0 || len(result.Notes) != 4 {
				t.Fatalf("failed optional reset-card query invented a metric or lost usage: %d %s", code, body)
			}
		})
	}
}

func TestQ8CLIOptionalErrorsKeepPrimaryAndSanitizedNotes(t *testing.T) {
	c := absorptionClient(t)
	c.HTTP = doerFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Query().Get("api"), "-usage") {
			return httpResponse(200, realUsageFixture), nil
		}
		return httpResponse(200, `{"code":"ConsoleNeedLogin","message":"Cookie=private-secret Authorization=private-secret"}`), nil
	})
	code, body := absorptionCLI(t, c, "--source", "cookie", "--cookie", "session=private-secret", "--verbose")
	var result Result
	if json.Unmarshal(body, &result) != nil || code != 0 || len(result.Windows) != 1 || result.Plan != "" || len(result.Metrics) != 0 || len(result.Notes) != 4 {
		t.Fatalf("optional login failure replaced primary reading: %d %s", code, body)
	}
	for _, expected := range []string{"Subscription", "add-on", "reset-card", "Coding Plan"} {
		if !strings.Contains(strings.Join(result.Notes, " "), expected) {
			t.Errorf("optional failure is silent: missing %s", expected)
		}
	}
}

func TestQ8CLICheckBskAndCookieOnlyVerifyLogin(t *testing.T) {
	for _, source := range []string{"bsk", "cookie"} {
		t.Run(source, func(t *testing.T) {
			c := absorptionClient(t)
			args := []string{"--source", source, "--check"}
			if source == "cookie" {
				args = append(args, "--cookie", "session=private-secret")
				absorptionPrimaryOnly(t, c, string(fixture(`{}`)))
			} else {
				payload, _ := json.Marshal(map[string]json.RawMessage{"usage": fixture(`{}`)})
				absorptionBrowser(t, c, `[{"instance_id":"responsive"}]`, "responsive", payload)
				run := c.Run
				c.Run = func(ctx context.Context, args ...string) ([]byte, error) {
					if args[0] == "evaluate" {
						for _, forbidden := range []string{
							"zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/subscription",
							"zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/reset-card/list",
							"zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2",
						} {
							if strings.Contains(args[1], url.QueryEscape(forbidden)) {
								t.Error("check requested enrichment", forbidden)
							}
						}
					}
					return run(ctx, args...)
				}
			}
			code, body := absorptionCLI(t, c, args...)
			var result Result
			if json.Unmarshal(body, &result) != nil || code != 0 || result.Source != source || len(result.Windows) != 0 || len(result.Metrics) != 0 || result.Plan != "" || len(result.Notes) != 1 {
				t.Fatalf("check invented usage: %d %s", code, body)
			}
		})
	}
}

func TestQ8CLIPrimaryFailureNeverFallsBackToCacheOrOtherSources(t *testing.T) {
	for _, source := range []string{"bsk", "cookie", "bl"} {
		t.Run(source, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "quota.json")
			original := []byte(`{"source":"cookie","observedAt":"2026-10-08T17:30:00+08:00","windows":[{"window":"1month","usedPercent":77}]}`)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			c := absorptionClient(t)
			args := []string{"--source", source, "--cache-file", path}
			want := ""
			switch source {
			case "bsk":
				want = "bsk_unavailable"
				c.Run = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("private-secret") }
			case "cookie":
				want = "network_error"
				args = append(args, "--cookie", "session=private-secret")
				c.HTTP = doerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("private-secret") })
			case "bl":
				want = "not_logged_in"
				c.RunBL = func(context.Context, string, ...string) ([]byte, []byte, error) {
					return nil, []byte(`{"error":{"code":3,"message":"private-secret"}}`), errors.New("private-secret")
				}
			}
			code, body := absorptionCLI(t, c, args...)
			if code != 1 {
				t.Fatalf("failed source emitted success: %d %s", code, body)
			}
			assertBLFailure(t, body, want)
			unchanged, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(unchanged, original) {
				t.Fatal("failed refresh changed last reading", err)
			}
		})
	}
}

func TestQ8CLICookieRedirectIsBlockedWithoutSocket(t *testing.T) {
	c := absorptionClient(t)
	underlying := NewClient().HTTP.(*http.Client)
	hits := 0
	underlying.Transport = codingTransport(func(req *http.Request) (*http.Response, error) {
		hits++
		if req.URL.Scheme != "https" || req.URL.Host != "bailian-cs.console.aliyun.com" || req.Header.Get("Cookie") != "session=private-secret" {
			t.Fatal("cookie escaped fixed console origin")
		}
		response := httpResponse(302, "private-secret")
		response.Header.Set("Location", "https://different-origin.invalid/receive-cookie")
		return response, nil
	})
	c.HTTP = underlying
	code, body := absorptionCLI(t, c, "--source", "cookie", "--cookie", "session=private-secret")
	if code != 1 || hits != 1 {
		t.Fatalf("redirect was followed or reported success: %d %s hits=%d", code, body, hits)
	}
	assertBLFailure(t, body, "not_logged_in")
}
