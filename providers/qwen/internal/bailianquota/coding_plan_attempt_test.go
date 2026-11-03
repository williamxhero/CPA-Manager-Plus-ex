package bailianquota

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCodingPlanAttemptHelperProcess(t *testing.T) {
	sidecar := os.Getenv("CODING_ATTEMPT_SIDECAR")
	if sidecar == "" {
		return
	}
	args := os.Args[len(os.Args)-4:]
	if !reflect.DeepEqual(args, []string{"usage", "coding-plan", "--output", "json"}) {
		os.Exit(2)
	}
	// Capture the public receipt at the actual child/query boundary, before
	// producing any reading. Missing running receipt must be visible to parent.
	running, err := os.ReadFile(sidecar)
	if err != nil {
		running = []byte("null")
	}
	if os.WriteFile(os.Getenv("CODING_ATTEMPT_CAPTURE"), running, 0600) != nil {
		os.Exit(2)
	}
	if os.Getenv("CODING_ATTEMPT_BLOCK_FINAL") == "1" {
		// Owned temporary-path fault injection at the public child boundary:
		// no product writer hook or permission/service/environment change.
		if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
			os.Exit(2)
		}
		if os.Mkdir(sidecar, 0700) != nil || os.WriteFile(filepath.Join(sidecar, "keep"), []byte("owned obstacle"), 0600) != nil {
			os.Exit(2)
		}
	}
	body, err := os.ReadFile(os.Getenv("CODING_TEST_BL_RESPONSE"))
	if err != nil {
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, string(body))
	code, _ := strconv.Atoi(os.Getenv("CODING_TEST_BL_EXIT"))
	os.Exit(code)
}

func codingPlanAttemptProcess(t *testing.T) (args []string, cache, sidecar, capture, response string) {
	t.Helper()
	shim, response, _ := codingBLProcess(t, codingBLFixture)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := "#!/bin/sh\nexec \"" + executable + "\" -test.run=^TestCodingPlanAttemptHelperProcess$ -- \"$@\"\n"
	if runtime.GOOS == "windows" {
		command = "@echo off\r\n\"" + executable + "\" -test.run=^TestCodingPlanAttemptHelperProcess$ -- %*\r\n"
	}
	if err := os.WriteFile(shim, []byte(command), 0700); err != nil {
		t.Fatal(err)
	}
	cache = filepath.Join(t.TempDir(), "coding-plan.json")
	sidecar, capture = cache+".attempt.json", filepath.Join(t.TempDir(), "running.json")
	t.Setenv("CODING_ATTEMPT_SIDECAR", sidecar)
	t.Setenv("CODING_ATTEMPT_CAPTURE", capture)
	t.Setenv("CODING_ATTEMPT_BLOCK_FINAL", "")
	return append(codingBLArgs(shim), "--cache-file", cache), cache, sidecar, capture, response
}

func readPublicAttempt(t *testing.T, path, state string) map[string]json.RawMessage {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("missing public attempt receipt", path, err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || string(fields["version"]) != "1" || string(fields["state"]) != `"`+state+`"` {
		t.Fatalf("invalid version/state: %s", body)
	}
	expected := map[string]bool{"version": true, "state": true, "startedAt": true}
	if state != "running" {
		expected["completedAt"] = true
	}
	if state == "success" {
		expected["observedAt"], expected["cacheSHA256"] = true, true
	}
	if len(fields) != len(expected) {
		t.Fatalf("missing or extra fields in %s receipt: %s", state, body)
	}
	for name := range fields {
		if !expected[name] {
			t.Fatalf("unexpected receipt field %s", name)
		}
	}
	for _, name := range []string{"startedAt", "completedAt", "observedAt"} {
		if raw, present := fields[name]; present {
			var value string
			if json.Unmarshal(raw, &value) != nil {
				t.Fatal("not a timestamp string", name)
			}
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				t.Fatal("invalid receipt time", name, err)
			}
		}
	}
	return fields
}

func TestCodingPlanAttemptSuccessPublishesRunningBeforeCLIAndMatchingCache(t *testing.T) {
	args, cache, sidecar, capture, _ := codingPlanAttemptProcess(t)
	var out, diagnostics bytes.Buffer
	if code := RunCLI(context.Background(), args, &out, &diagnostics); code != 0 {
		t.Fatal("synthetic CLI query failed", code, out.String())
	}
	reading := assertCodingBLReading(t, out.Bytes())
	running := readPublicAttempt(t, capture, "running")
	completed := readPublicAttempt(t, sidecar, "success")
	if !bytes.Equal(running["startedAt"], completed["startedAt"]) {
		t.Fatal("completion is not the same query attempt")
	}
	saved, err := os.ReadFile(cache)
	if err != nil || !bytes.Equal(saved, out.Bytes()) {
		t.Fatal("cache differs from success stdout", err)
	}
	sum := sha256.Sum256(saved)
	wantHash, _ := json.Marshal(hex.EncodeToString(sum[:]))
	wantObserved, _ := json.Marshal(reading.ObservedAt)
	if !bytes.Equal(completed["cacheSHA256"], wantHash) || !bytes.Equal(completed["observedAt"], wantObserved) || diagnostics.Len() != 0 {
		t.Fatal("success receipt does not bind the exact cache and original observation", string(completed["cacheSHA256"]))
	}
	// Cache-only is still a non-producing read, even with no receipt access.
	before, _ := os.ReadFile(sidecar)
	out.Reset()
	if code := RunCLI(context.Background(), append(args, "--cache-only"), &out, io.Discard); code != 0 || !bytes.Equal(saved, out.Bytes()) {
		t.Fatal("cache-only changed existing behavior", code, out.String())
	}
	after, _ := os.ReadFile(sidecar)
	if !bytes.Equal(before, after) {
		t.Fatal("cache-only modified the attempt sidecar")
	}
}

func TestCodingPlanAttemptFailedRefreshInvalidatesRecentSuccessWithoutChangingCache(t *testing.T) {
	args, cache, sidecar, capture, response := codingPlanAttemptProcess(t)
	var out, diagnostics bytes.Buffer
	if code := RunCLI(context.Background(), args, &out, &diagnostics); code != 0 {
		t.Fatal(out.String())
	}
	original, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	readPublicAttempt(t, sidecar, "success")
	if err := os.WriteFile(response, []byte(`{"error":{"code":3,"message":"private-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODING_TEST_BL_EXIT", "3")
	out.Reset()
	if code := RunCLI(context.Background(), args, &out, &diagnostics); code != 1 {
		t.Fatal("failed refresh became success", code, out.String())
	}
	assertBLFailure(t, out.Bytes(), "not_logged_in")
	running := readPublicAttempt(t, capture, "running")
	failed := readPublicAttempt(t, sidecar, "failed")
	if !bytes.Equal(running["startedAt"], failed["startedAt"]) {
		t.Fatal("failure not bound to current attempt")
	}
	unchanged, _ := os.ReadFile(cache)
	if !bytes.Equal(original, unchanged) {
		t.Fatal("failed refresh mutated prior successful cache")
	}
	receiptBefore, _ := os.ReadFile(sidecar)
	queryBefore, _ := os.ReadFile(capture)
	out.Reset()
	if code := RunCLI(context.Background(), append(args, "--cache-only"), &out, &diagnostics); code != 0 || !bytes.Equal(original, out.Bytes()) {
		t.Fatal("cache-only lost its existing behavior", code, out.String())
	}
	receiptAfter, _ := os.ReadFile(sidecar)
	queryAfter, _ := os.ReadFile(capture)
	if !bytes.Equal(receiptBefore, receiptAfter) || !bytes.Equal(queryBefore, queryAfter) || strings.Contains(string(receiptAfter)+diagnostics.String(), "private-secret") {
		t.Fatal("cache-only changed receipt/queried CLI or failure leaked diagnostics")
	}
}

func TestCodingPlanAttemptStartPublicationFailureNeverQueriesOrChangesPriorCache(t *testing.T) {
	args, cache, sidecar, capture, _ := codingPlanAttemptProcess(t)
	var out, diagnostics bytes.Buffer
	if code := RunCLI(context.Background(), args, &out, &diagnostics); code != 0 {
		t.Fatal(out.String())
	}
	original, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(sidecar, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "keep"), []byte("owned obstacle"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(capture); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := RunCLI(context.Background(), args, &out, &diagnostics); code != 1 {
		t.Fatalf("receipt publication failure allowed a query/success: exit=%d output=%s", code, out.String())
	}
	assertBLFailure(t, out.Bytes(), "cache_attempt_error")
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("CLI ran despite start-publication failure", err)
	}
	unchanged, _ := os.ReadFile(cache)
	if !bytes.Equal(original, unchanged) || strings.Contains(out.String(), `"windows"`) || diagnostics.Len() != 0 {
		t.Fatal("publication failure mutated prior cache, emitted success or leaked diagnostics")
	}
	assertNoAttemptTemps(t, cache)
}

func TestCodingPlanAttemptFinalPublicationFailureCannotEmitSuccess(t *testing.T) {
	args, cache, sidecar, capture, _ := codingPlanAttemptProcess(t)
	t.Setenv("CODING_ATTEMPT_BLOCK_FINAL", "1")
	var out, diagnostics bytes.Buffer
	if code := RunCLI(context.Background(), args, &out, &diagnostics); code != 1 {
		t.Fatalf("failed final receipt emitted success: exit=%d output=%s", code, out.String())
	}
	assertBLFailure(t, out.Bytes(), "cache_attempt_error")
	readPublicAttempt(t, capture, "running")
	// Cache commit precedes final receipt. This is deliberately not claimed as
	// cross-file rollback/atomicity: Core must reject this invalid receipt pair.
	saved, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	assertCodingBLReading(t, saved)
	info, err := os.Stat(sidecar)
	if err != nil || !info.IsDir() || strings.Contains(out.String(), `"windows"`) || diagnostics.Len() != 0 {
		t.Fatal("invalid receipt pair became success or leaked diagnostics", err)
	}
	assertNoAttemptTemps(t, cache)
}

func TestCodingPlanAttemptCacheWriteFailurePublishesFailed(t *testing.T) {
	args, cache, sidecar, _, _ := codingPlanAttemptProcess(t)
	if err := os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := RunCLI(context.Background(), args, &out, io.Discard); code != 1 {
		t.Fatal("cache write failure became success", out.String())
	}
	assertBLFailure(t, out.Bytes(), "cache_file_error")
	readPublicAttempt(t, sidecar, "failed")
	assertNoAttemptTemps(t, cache)
}

func TestCodingPlanAttemptStdoutFailurePublishesFailed(t *testing.T) {
	args, cache, sidecar, _, _ := codingPlanAttemptProcess(t)
	if code := RunCLI(context.Background(), args, failingWriter{}, io.Discard); code != 1 {
		t.Fatal("stdout failure became success", code)
	}
	readPublicAttempt(t, sidecar, "failed")
	assertNoAttemptTemps(t, cache)
}

func assertNoAttemptTemps(t *testing.T, cache string) {
	t.Helper()
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(cache), ".coding-plan-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal("attempt/cache publication left temporary files", leftovers, err)
	}
}
