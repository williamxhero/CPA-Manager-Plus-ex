package bailianquota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// Execute only the explicit official usage command. No helper auth commands,
// retries, browser fallback, or Token Plan query. The supplied CLI's own auth
// policy is outside this transport: some official versions refresh internally.
func (c *Client) fetchCodingPlanBL(ctx context.Context, executable string) (CodingPlanResult, error) {
	empty := CodingPlanResult{}
	run := c.RunBL
	if run == nil {
		run = runBL
	}
	stdout, stderr, runErr := run(ctx, executable, "usage", "coding-plan", "--output", "json")
	if ctx.Err() != nil || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled) {
		return empty, failure("timeout", "coding_plan_cli_canceled_or_timed_out")
	}
	var known *Failure
	if errors.As(runErr, &known) {
		return empty, runErr
	}
	if len(stdout) > maxResponseBytes || len(stderr) > maxResponseBytes {
		return empty, failure("invalid_response", "coding_plan_cli_output_exceeds_response_limit")
	}
	var coded exitCoder
	exitCode := 0
	if errors.As(runErr, &coded) {
		exitCode = coded.ExitCode()
	}
	stdoutCode, stdoutError := blError(stdout)
	stderrCode, stderrError := blError(stderr)
	if stdoutCode == 3 || stderrCode == 3 || exitCode == 3 {
		return empty, failure("not_logged_in", "coding_plan_cli_console_credentials_unavailable; no helper auth retry")
	}
	if stdoutCode == 5 || stderrCode == 5 || exitCode == 5 {
		return empty, failure("timeout", "coding_plan_cli_exceeded_deadline")
	}
	if stdoutError || stderrError || runErr != nil {
		return empty, failure("invalid_response", "coding_plan_cli_failed")
	}
	var usage struct {
		InstanceType string               `json:"instanceType"`
		Per5Hour     *codingPlanCLIWindow `json:"per5Hour"`
		PerWeek      *codingPlanCLIWindow `json:"perWeek"`
		PerBillMonth *codingPlanCLIWindow `json:"perBillMonth"`
	}
	// Unlike legacy monthly BL, the strict Coding Plan contract accepts no
	// teardown assertion trailer or concatenated payload after its JSON value.
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&usage) != nil {
		return empty, failure("invalid_quota", "coding_plan_cli_reading_invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return empty, failure("invalid_quota", "coding_plan_cli_reading_invalid")
	}
	quota := make(map[string]any)
	for _, field := range []struct {
		prefix string
		window *codingPlanCLIWindow
	}{{"per5Hour", usage.Per5Hour}, {"perWeek", usage.PerWeek}, {"perBillMonth", usage.PerBillMonth}} {
		if field.window == nil || field.window.Percentage != nil && (*field.window.Percentage < 0 || *field.window.Percentage > 1) {
			return empty, failure("invalid_quota", "coding_plan_cli_window_missing_or_invalid")
		}
		quota[field.prefix+"UsedQuota"] = field.window.UsedQuota
		quota[field.prefix+"TotalQuota"] = field.window.TotalQuota
		quota[field.prefix+"QuotaNextRefreshTime"] = field.window.ResetTime
	}
	return codingPlanReading(quota, usage.InstanceType, c.Now())
}

type codingPlanCLIWindow struct {
	UsedQuota  any      `json:"usedQuota"`
	TotalQuota any      `json:"totalQuota"`
	Percentage *float64 `json:"percentage"` // Informational only; never fills missing native counts.
	ResetTime  any      `json:"resetTime"`
}
