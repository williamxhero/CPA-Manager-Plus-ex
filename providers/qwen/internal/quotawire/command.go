// Package quotawire is the common validated CLI boundary for Qwen and the
// quota-only compatibility plugin. It never interprets a reading as ownership.
package quotawire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cpa-credential-qwen/internal/errclass"
)

const MaxStdout = 1 << 20
const MaxStderr = 16 << 10
const MaxDiagnostic = 4096

type boundedOutput struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if remaining := b.limit - b.buf.Len(); len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
		b.cancel()
	}
	_, _ = b.buf.Write(p)
	return n, nil
}

func (b *boundedOutput) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...), b.overflow
}

// Run uses argument slices, not a shell. Secrets are used only for redaction;
// selected API credentials must never be added to the command or environment.
func Run(ctx context.Context, command string, args []string, workdir string, timeout time.Duration, secrets []string) ([]byte, error) {
	out, _, err := RunWithDiagnostics(ctx, command, args, workdir, timeout, secrets)
	return out, err
}

// RunWithDiagnostics additionally retains sanitized, bounded stderr for callers
// that must preserve the helper's classification when stdout fails decoding.
// Valid stdout may have ordinary stderr warnings; they are not a second result.
func RunWithDiagnostics(ctx context.Context, command string, args []string, workdir string, timeout time.Duration, secrets []string) ([]byte, []byte, error) {
	if command == "" || timeout <= 0 {
		return nil, nil, errors.New("quota command is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout := &boundedOutput{limit: MaxStdout, cancel: cancel}
	stderr := &boundedOutput{limit: MaxStderr, cancel: cancel}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = workdir
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	out, outOverflow := stdout.snapshot()
	errOut, errOverflow := stderr.snapshot()
	failure := func(message string) ([]byte, []byte, error) {
		return nil, nil, errors.New(Sanitize(message, args, secrets))
	}
	if outOverflow || errOverflow {
		return failure("quota command output exceeds byte limit")
	}
	if ctx.Err() != nil {
		return failure("quota command timed out or was canceled")
	}
	if err != nil {
		return failure(Diagnostic(out, errOut, "quota command failed"))
	}
	var reported struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(out, &reported) == nil && reported.Error != "" {
		return failure(Diagnostic(out, errOut, "quota command returned an error"))
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return failure(Diagnostic(nil, errOut, "quota command returned no valid JSON on stdout"))
	}
	return out, []byte(Sanitize(string(errOut), args, secrets)), nil
}

// Diagnostic preserves the CLI's own error classification without embedding a
// second copy of its full JSON document in an ABI error. Call Sanitize before
// exposing this diagnostic outside a caller's trusted boundary.
func Diagnostic(stdout, stderr []byte, fallback string) string {
	var failure struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(stdout, &failure) == nil {
		if failure.Message != "" {
			if failure.Error != "" {
				return failure.Error + ": " + failure.Message
			}
			return failure.Message
		}
		if failure.Error != "" {
			return failure.Error
		}
		if len(bytes.TrimSpace(stderr)) > 0 {
			return strings.TrimSpace(string(stderr))
		}
		return fallback
	}
	if len(bytes.TrimSpace(stdout)) > 0 {
		return strings.TrimSpace(string(stdout))
	}
	if len(bytes.TrimSpace(stderr)) > 0 {
		return strings.TrimSpace(string(stderr))
	}
	return fallback
}

var secretKey = regexp.MustCompile(`\bsk-[A-Za-z0-9_.-]+`)
var secretField = regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?key[_-]?(?:id|secret)|access[_-]?token|refresh[_-]?token|token|password|secret|cookie|set-cookie)["']*\s*[:=]\s*["']?)[^\r\n"']+`)

func Sanitize(message string, args []string, secrets []string) string {
	values := append([]string(nil), secrets...)
	for i, arg := range args {
		for _, flag := range []string{"--cookie", "--token", "--api-key", "--password", "--secret", "--access-key-id", "--access-key-secret"} {
			lower := strings.ToLower(arg)
			if lower == flag && i+1 < len(args) {
				values = append(values, args[i+1])
			}
			if strings.HasPrefix(lower, flag+"=") {
				values = append(values, arg[len(flag)+1:])
			}
		}
	}
	variants := make(map[string]bool)
	for _, value := range values {
		if value == "" {
			continue
		}
		encoded, form := url.PathEscape(value), url.QueryEscape(value)
		for _, variant := range []string{value, encoded, form, url.PathEscape(encoded), url.PathEscape(form), url.QueryEscape(encoded), url.QueryEscape(form)} {
			if !utf8.ValidString(variant) {
				// In-process metadata is not necessarily decoded from JSON.
				// RE2 interprets invalid input bytes as RuneError. Compile the
				// same rune sequence so invalid and valid variants are still
				// replaced in a single pass, never reprocessing a new marker.
				variant = string([]rune(variant))
			}
			variants[variant] = true
		}
	}
	ordered := make([]string, 0, len(variants))
	for variant := range variants {
		ordered = append(ordered, variant)
	}
	// Match all known forms in one pass: reprocessing [redacted] can disclose
	// partial values or expand output when a short secret occurs in the marker.
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	patternBytes := 0
	for i, variant := range ordered {
		ordered[i] = regexp.QuoteMeta(variant)
		patternBytes += len(ordered[i]) + 1
		if patternBytes > MaxStdout {
			return "quota command failed (sensitive diagnostic suppressed)"
		}
	}
	if len(ordered) > 0 {
		// Percent hex is case-insensitive, including mixed case and a
		// second encoding layer. Prefer over-redaction to disclosing keys.
		pattern, err := regexp.Compile("(?i)(?:" + strings.Join(ordered, "|") + ")")
		if err != nil {
			return "quota command failed (sensitive diagnostic suppressed)"
		}
		message = pattern.ReplaceAllString(message, "[redacted]")
	}
	message = secretField.ReplaceAllString(secretKey.ReplaceAllString(errclass.Redact(message), "[redacted]"), "${1}[redacted]")
	return BoundDiagnostic([]byte(message))
}

func BoundDiagnostic(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > MaxDiagnostic {
		text = text[:MaxDiagnostic] + " [truncated]"
	}
	return text
}
