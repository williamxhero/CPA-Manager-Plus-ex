package bailianquota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Public first-party vector: modelstudioai/cli@a7cb810, usage/coding-plan.ts
// readUsage/readWindow and coding-plan-usage.test.ts. No account information.
const codingBLFixture = `{"instanceType":"pro","per5Hour":{"usedQuota":38,"totalQuota":100,"percentage":0.38,"resetTime":1786000000000},"perWeek":{"usedQuota":500,"totalQuota":1000,"percentage":0.5,"resetTime":1786100000000},"perBillMonth":{"usedQuota":950,"totalQuota":1000,"percentage":0.95,"resetTime":1786200000000}}`

func TestCodingPlanBLHelperProcess(t *testing.T) {
	path := os.Getenv("CODING_TEST_BL_RESPONSE")
	if path == "" {
		return
	}
	args := os.Args[len(os.Args)-4:]
	body, _ := json.Marshal(args)
	marker, err := os.OpenFile(os.Getenv("CODING_TEST_BL_ARGS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	_, err = marker.Write(append(body, '\n'))
	closeErr := marker.Close()
	if err != nil || closeErr != nil {
		os.Exit(2)
	}
	if !reflect.DeepEqual(args, []string{"usage", "coding-plan", "--output", "json"}) {
		os.Exit(2)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, string(body))
	if path := os.Getenv("CODING_TEST_BL_STDERR"); path != "" {
		body, _ = os.ReadFile(path)
		fmt.Fprint(os.Stderr, string(body))
	}
	code, _ := strconv.Atoi(os.Getenv("CODING_TEST_BL_EXIT"))
	os.Exit(code)
}

func codingBLProcess(t *testing.T, response string) (shim, responseFile, calls string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	responseFile, calls = filepath.Join(dir, "response.json"), filepath.Join(dir, "calls.json")
	if err := os.WriteFile(responseFile, []byte(response), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODING_TEST_BL_RESPONSE", responseFile)
	t.Setenv("CODING_TEST_BL_ARGS", calls)
	t.Setenv("CODING_TEST_BL_STDERR", "")
	t.Setenv("CODING_TEST_BL_EXIT", "0")
	shim, command := filepath.Join(dir, "synthetic bl.sh"), "#!/bin/sh\nexec \""+executable+"\" -test.run=^TestCodingPlanBLHelperProcess$ -- \"$@\"\n"
	if runtime.GOOS == "windows" {
		shim, command = filepath.Join(dir, "synthetic bl.cmd"), "@echo off\r\n\""+executable+"\" -test.run=^TestCodingPlanBLHelperProcess$ -- %*\r\n"
	}
	if err := os.WriteFile(shim, []byte(command), 0700); err != nil {
		t.Fatal(err)
	}
	useCodingTransport(t, func(*http.Request) (*http.Response, error) {
		t.Error("CLI/cache lane attempted direct HTTP")
		return nil, io.EOF
	})
	return
}

func codingBLArgs(shim string) []string {
	return []string{"--protocol", "qwen-coding-plan-v1", "--source", "coding-plan", "--bl-path", shim}
}

func assertCodingBLReading(t *testing.T, out []byte) CodingPlanResult {
	t.Helper()
	result, err := DecodeCodingPlanResult(out)
	if err != nil {
		t.Fatalf("invalid public helper contract: %s (%v)", out, err)
	}
	if result.InstanceType != "pro" || result.Windows[0].Used != "38" || result.Windows[0].Total != "100" || result.Windows[0].Remaining != "62" || result.Windows[0].ResetTime != "2026-08-06T07:06:40Z" || result.Windows[1].Remaining != "500" || result.Windows[2].Remaining != "50" {
		t.Fatalf("wrong official Coding Plan vector: %s", out)
	}
	if strings.Contains(string(out), "verified") && strings.Contains(string(out), `"verified":true`) {
		t.Fatal("query became proof", string(out))
	}
	return result
}

func TestCodingPlanCLIUsesOfficialBLThreeWindows(t *testing.T) {
	shim, _, calls := codingBLProcess(t, codingBLFixture)
	var out, diagnostics bytes.Buffer
	if code := RunCLI(context.Background(), codingBLArgs(shim), &out, &diagnostics); code != 0 {
		t.Fatalf("explicit official CLI Coding Plan transport unavailable: exit=%d output=%s", code, out.String())
	}
	assertCodingBLReading(t, out.Bytes())
	raw, err := os.ReadFile(calls)
	if err != nil || strings.TrimSpace(string(raw)) != `["usage","coding-plan","--output","json"]` {
		t.Fatal("wrong product/auth command", string(raw), err)
	}
	if diagnostics.Len() != 0 {
		t.Fatal("unexpected raw CLI diagnostics", diagnostics.String())
	}
}

func TestCodingPlanCLICacheRoundTripAndFailedRefresh(t *testing.T) {
	shim, response, calls := codingBLProcess(t, codingBLFixture)
	cache := filepath.Join(t.TempDir(), "coding-plan.json")
	args := append(codingBLArgs(shim), "--cache-file", cache)
	var out, diagnostics bytes.Buffer
	if code := RunCLI(context.Background(), args, &out, &diagnostics); code != 0 {
		t.Fatalf("Coding Plan CLI/cache refresh unavailable: exit=%d output=%s", code, out.String())
	}
	reading := assertCodingBLReading(t, out.Bytes())
	if _, err := time.Parse(time.RFC3339Nano, reading.ObservedAt); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(cache)
	if err != nil || !bytes.Equal(original, out.Bytes()) {
		t.Fatal("cache does not match the complete reading", err)
	}
	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODING_TEST_BL_EXIT", "3") // Any accidental cache-only CLI invocation fails.
	for _, offlineArgs := range [][]string{
		append(args, "--cache-only", "--max-age", "45m"),
		{"--protocol", CodingPlanProtocol, "--source", "coding-plan", "--cache-only", "--cache-file", cache},
	} {
		out.Reset()
		if code := RunCLI(context.Background(), offlineArgs, &out, &diagnostics); code != 0 || !bytes.Equal(original, out.Bytes()) {
			t.Fatalf("cache-only changed observation or queried the CLI: exit=%d output=%s", code, out.String())
		}
		if _, err := os.Stat(calls); !os.IsNotExist(err) {
			t.Fatal("cache-only started a subprocess", err)
		}
	}
	if err := os.WriteFile(response, []byte(`{"error":{"code":3,"message":"private-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := RunCLI(context.Background(), args, &out, &diagnostics); code != 1 {
		t.Fatal("failed refresh became success", code, out.String())
	}
	assertBLFailure(t, out.Bytes(), "not_logged_in")
	unchanged, err := os.ReadFile(cache)
	if err != nil || !bytes.Equal(original, unchanged) {
		t.Fatal("failed refresh replaced the last good reading", err)
	}
	commands, _ := os.ReadFile(calls)
	if bytes.Count(commands, []byte("\n")) != 1 || strings.Contains(out.String()+diagnostics.String(), "private-secret") {
		t.Fatal("automatic retry or raw diagnostic leak", string(commands), out.String())
	}
}

func TestCodingPlanCLINonzeroTeardownCannotBecomeReading(t *testing.T) {
	shim, _, calls := codingBLProcess(t, codingBLFixture)
	// Legacy monthly BL tolerates this Windows teardown status. The strict
	// Coding Plan protocol must not inherit that nonzero-exit exception.
	t.Setenv("CODING_TEST_BL_EXIT", "3221226505")
	var out, diagnostics bytes.Buffer
	if code := RunCLI(context.Background(), codingBLArgs(shim), &out, &diagnostics); code != 1 {
		t.Fatalf("nonzero CLI status became a Coding Plan reading: exit=%d output=%s", code, out.String())
	}
	assertBLFailure(t, out.Bytes(), "invalid_response")
	commands, _ := os.ReadFile(calls)
	if bytes.Count(commands, []byte("\n")) != 1 || diagnostics.Len() != 0 {
		t.Fatal("unexpected retry or raw diagnostics", string(commands), diagnostics.String())
	}
}

func TestCodingPlanCLITeardownTrailerCannotBecomeReading(t *testing.T) {
	shim, _, _ := codingBLProcess(t, codingBLFixture+"\nAssertion failed: UV_HANDLE_CLOSING\n")
	var out bytes.Buffer
	if code := RunCLI(context.Background(), codingBLArgs(shim), &out, io.Discard); code != 1 || strings.Contains(out.String(), `"windows"`) {
		t.Fatalf("legacy text trailer became strict Coding Plan JSON: exit=%d output=%s", code, out.String())
	}
}

func TestCodingPlanCLICacheRejectsUnsafeReadingsOffline(t *testing.T) {
	shim, _, calls := codingBLProcess(t, codingBLFixture)
	observed := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	valid := `{"protocol":"qwen-coding-plan-v1","product":"coding-plan","queryStatus":"success","authorityStatus":"needs_configuration","reason":"account_authority_unverified","observedAt":"` + observed + `","instanceType":"pro","windows":[{"window":"5h","unit":"requests","used":"38","total":"100","remaining":"62","resetTime":"2026-08-06T07:06:40Z"},{"window":"week","unit":"requests","used":"500","total":"1000","remaining":"500","resetTime":"2026-08-07T10:53:20Z"},{"window":"billing_month","unit":"requests","used":"950","total":"1000","remaining":"50","resetTime":"2026-08-08T14:40:00Z"}]}`
	cache := filepath.Join(t.TempDir(), "untrusted-cache.json")
	for name, body := range map[string]string{
		"stale":             strings.Replace(valid, observed, time.Now().UTC().Add(-2*time.Hour).Format(time.RFC3339Nano), 1),
		"future":            strings.Replace(valid, observed, time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano), 1),
		"wrong_protocol":    strings.Replace(valid, CodingPlanProtocol, "qwen-token-plan-v1", 1),
		"legacy_token_plan": `{"source":"bl","observedAt":"` + observed + `","windows":[{"window":"1month","usedPercent":38}]}`,
		"malformed":         `{"protocol":`,
		"partial":           strings.Replace(valid, `"used":"38"`, `"used":null`, 1),
		"wrong_unit":        strings.Replace(valid, `"unit":"requests"`, `"unit":"tokens"`, 1),
		"authority_claim":   strings.Replace(valid, `"authorityStatus":"needs_configuration"`, `"authorityStatus":"verified"`, 1),
		"extra_verified":    strings.Replace(valid, `"protocol":`, `"verified":true,"protocol":`, 1),
		"failed_query":      strings.Replace(valid, `"queryStatus":"success"`, `"queryStatus":"failed"`, 1),
		"trailing":          valid + `{}`,
		"oversized":         strings.Repeat(" ", maxResponseBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(cache, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			var out, diagnostics bytes.Buffer
			args := append(codingBLArgs(shim), "--cache-file", cache, "--cache-only")
			if code := RunCLI(context.Background(), args, &out, &diagnostics); code != 1 || strings.Contains(out.String(), `"windows"`) {
				t.Fatalf("unsafe cache became reading: exit=%d output=%s", code, out.String())
			}
			want := "invalid_cache"
			if name == "stale" {
				want = "stale_cache"
			} else if name == "oversized" {
				want = "cache_file_error"
			}
			assertBLFailure(t, out.Bytes(), want)
			if _, err := os.Stat(calls); !os.IsNotExist(err) || diagnostics.Len() != 0 {
				t.Fatal("cache-only invoked CLI or exposed diagnostics", err, diagnostics.String())
			}
			unchanged, _ := os.ReadFile(cache)
			if string(unchanged) != body {
				t.Fatal("cache read changed the supplied bytes")
			}
		})
	}
}

func TestCodingPlanCLIRejectsUnsafeFlattenedBLReadings(t *testing.T) {
	for name, body := range map[string]string{
		"missing_count":      strings.Replace(codingBLFixture, `"usedQuota":38,`, "", 1),
		"null_count":         strings.Replace(codingBLFixture, `"usedQuota":38`, `"usedQuota":null`, 1),
		"string_count":       strings.Replace(codingBLFixture, `"usedQuota":38`, `"usedQuota":"38"`, 1),
		"fraction_count":     strings.Replace(codingBLFixture, `"usedQuota":38`, `"usedQuota":38.5`, 1),
		"seconds":            strings.Replace(codingBLFixture, "1786000000000", "1786000000", 1),
		"fractional_millis":  strings.Replace(codingBLFixture, "1786000000000", "1786000000000.5", 1),
		"missing_window":     strings.Replace(codingBLFixture, `"per5Hour":{"usedQuota":38,"totalQuota":100,"percentage":0.38,"resetTime":1786000000000}`, `"per5Hour":null`, 1),
		"nested_error":       strings.Replace(codingBLFixture, `"per5Hour":{`, `"per5Hour":{"error":{"code":3,"message":"private-secret"},`, 1),
		"trailing":           codingBLFixture + `{}`,
		"auth":               `{"error":{"code":3,"message":"private-secret"}}`,
		"monthly_token_plan": `{"per1MonthPercentage":0.38,"per1MonthResetTime":1786000000000}`,
	} {
		t.Run(name, func(t *testing.T) {
			shim, _, calls := codingBLProcess(t, body)
			var out, diagnostics bytes.Buffer
			if code := RunCLI(context.Background(), codingBLArgs(shim), &out, &diagnostics); code != 1 || strings.Contains(out.String(), `"windows"`) || strings.Contains(out.String()+diagnostics.String(), "private-secret") {
				t.Fatalf("unsafe CLI output became reading or leaked: exit=%d output=%s", code, out.String())
			}
			commands, _ := os.ReadFile(calls)
			if bytes.Count(commands, []byte("\n")) != 1 {
				t.Fatal("unexpected retry/fallback", string(commands))
			}
		})
	}
}

func TestCodingPlanCLITokenCacheRemainsOfflineAfterTokenRemoval(t *testing.T) {
	args, token := codingCLIArgs(t)
	cache := filepath.Join(t.TempDir(), "token-transport-coding-cache.json")
	args = append(args, "--cache-file", cache)
	calls := 0
	useCodingTransport(t, func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(codingResponse(codingQuotaFixture)))}, nil
	})
	var out bytes.Buffer
	if code := RunCLI(context.Background(), args, &out, io.Discard); code != 0 || calls != 1 {
		t.Fatal("raw token mode cannot save strict Coding Plan cache", out.String(), calls)
	}
	original := append([]byte(nil), out.Bytes()...)
	if err := os.Remove(token); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := RunCLI(context.Background(), append(args, "--cache-only"), &out, io.Discard); code != 0 || calls != 1 || !bytes.Equal(original, out.Bytes()) {
		t.Fatal("cache-only reread credentials/network or changed observation", out.String(), calls)
	}
}

func TestCodingPlanCLICacheReplacesCompleteReadingAndCleansFailedWrite(t *testing.T) {
	shim, response, _ := codingBLProcess(t, codingBLFixture)
	dir := t.TempDir()
	cache := filepath.Join(dir, "coding-plan.json")
	args := append(codingBLArgs(shim), "--cache-file", cache)
	var out bytes.Buffer
	if code := RunCLI(context.Background(), args, &out, io.Discard); code != 0 {
		t.Fatal(out.String())
	}
	updated := strings.Replace(codingBLFixture, `"usedQuota":38`, `"usedQuota":39`, 1)
	updated = strings.Replace(updated, `"percentage":0.38`, `"percentage":0.39`, 1)
	if err := os.WriteFile(response, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := RunCLI(context.Background(), args, &out, io.Discard); code != 0 {
		t.Fatal("could not replace existing cache", out.String())
	}
	reading, err := DecodeCodingPlanResult(out.Bytes())
	saved, readErr := os.ReadFile(cache)
	if err != nil || readErr != nil || reading.Windows[0].Remaining != "61" || !bytes.Equal(saved, out.Bytes()) {
		t.Fatal("replacement was incomplete or stale", out.String(), err, readErr)
	}
	// Existing nonempty directory cannot be atomically replaced. Save failure
	// must be honest, preserve both prior cache/sentinel and remove temporary files.
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(blocked, "keep")
	if err := os.WriteFile(sentinel, []byte("prior bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := RunCLI(context.Background(), append(codingBLArgs(shim), "--cache-file", blocked), &out, io.Discard); code != 1 {
		t.Fatal("failed save returned successful reading", out.String())
	}
	assertBLFailure(t, out.Bytes(), "cache_file_error")
	unchanged, _ := os.ReadFile(cache)
	keep, _ := os.ReadFile(sentinel)
	leftovers, err := filepath.Glob(filepath.Join(dir, ".coding-plan-*"))
	if !bytes.Equal(saved, unchanged) || string(keep) != "prior bytes" || err != nil || len(leftovers) != 0 {
		t.Fatal("failed atomic write damaged old files or left temporary files", leftovers, err)
	}
}

func TestCodingPlanCLIStderrAuthErrorOverridesReading(t *testing.T) {
	shim, _, _ := codingBLProcess(t, codingBLFixture)
	stderr := filepath.Join(t.TempDir(), "stderr.json")
	if err := os.WriteFile(stderr, []byte(`{"error":{"code":3,"message":"private-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODING_TEST_BL_STDERR", stderr)
	var out, diagnostics bytes.Buffer
	if code := RunCLI(context.Background(), codingBLArgs(shim), &out, &diagnostics); code != 1 {
		t.Fatal("stderr auth failure became success", out.String())
	}
	assertBLFailure(t, out.Bytes(), "not_logged_in")
	if strings.Contains(out.String()+diagnostics.String(), "private-secret") {
		t.Fatal("raw stderr leaked")
	}
}

func TestCodingPlanCLIBLExactCountsAndMilliseconds(t *testing.T) {
	body := strings.Replace(codingBLFixture, `"usedQuota":38,"totalQuota":100`, `"usedQuota":9007199254740993,"totalQuota":9007199254740995`, 1)
	body = strings.Replace(body, "1786000000000", "1786000000123", 1)
	shim, _, _ := codingBLProcess(t, body)
	var out bytes.Buffer
	if code := RunCLI(context.Background(), codingBLArgs(shim), &out, io.Discard); code != 0 {
		t.Fatal(out.String())
	}
	for _, want := range []string{`"used":"9007199254740993"`, `"total":"9007199254740995"`, `"remaining":"2"`, `"resetTime":"2026-08-06T07:06:40.123Z"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("helper rounded native CLI count/time (%s): %s", want, out.String())
		}
	}
}

func TestCodingPlanCLITransportAndCacheConfigurationFailClosed(t *testing.T) {
	shim, _, calls := codingBLProcess(t, codingBLFixture)
	t.Setenv("BAILIAN_BL_PATH", shim)
	base := []string{"--protocol", CodingPlanProtocol, "--source", "coding-plan"}
	for _, extra := range [][]string{
		nil, // The environment alone must not opt this lane into the CLI/profile.
		{"--bl-path", ""},
		{"--bl-path", shim, "--token-file", "must-not-read"},
		{"--bl-path", shim, "--console-site", "domestic"},
		{"--bl-path", shim, "--cache-only"},
		{"--cache-only", "--cache-file", "must-not-read", "--max-age", "0s"},
	} {
		var out bytes.Buffer
		if code := RunCLI(context.Background(), append(base, extra...), &out, io.Discard); code != 1 {
			t.Fatal("implicit or conflicting configuration accepted", extra, out.String())
		}
		assertBLFailure(t, out.Bytes(), "needs_configuration")
		if _, err := os.Stat(calls); !os.IsNotExist(err) {
			t.Fatal("invalid configuration invoked CLI", extra, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if code := RunCLI(ctx, codingBLArgs(shim), &out, io.Discard); code != 1 {
		t.Fatal("canceled operation became success", out.String())
	}
	assertBLFailure(t, out.Bytes(), "timeout")
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatal("pre-canceled operation started CLI", err)
	}
}
