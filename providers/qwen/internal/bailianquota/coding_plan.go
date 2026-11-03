package bailianquota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const CodingPlanProtocol = "qwen-coding-plan-v1"
const codingPlanAPI = "zeldaEasy.broadscope-bailian.codingPlan.queryCodingPlanInstanceInfoV2"

// This is our helper protocol, not an assertion of official account identity.
// Counts are exact decimal strings, never float64 or formatted percentages.
type CodingPlanResult struct {
	Protocol        string             `json:"protocol"`
	Product         string             `json:"product"`
	QueryStatus     string             `json:"queryStatus"`
	AuthorityStatus string             `json:"authorityStatus"`
	Reason          string             `json:"reason"`
	ObservedAt      string             `json:"observedAt"`
	InstanceType    string             `json:"instanceType,omitempty"`
	Windows         []CodingPlanWindow `json:"windows"`
}

type CodingPlanWindow struct {
	Window    string `json:"window"`
	Unit      string `json:"unit"`
	Used      string `json:"used"`
	Total     string `json:"total"`
	Remaining string `json:"remaining"`
	ResetTime string `json:"resetTime"`
}

// No CLI execution, default profile, browser, cookie, enrollment, refresh,
// configuration/cache write, redirect, or arbitrary URL/operation is reachable.
func (c *Client) fetchCodingPlan(ctx context.Context, tokenFile, region, site string) (CodingPlanResult, error) {
	empty := CodingPlanResult{}
	var host, action, commodity string
	switch {
	case region == "cn-beijing" && site == "domestic":
		host, action, commodity = "bailian-cs.console.aliyun.com", "BroadScopeAspnGateway", "sfm_codingplan_public_cn"
	case region == "ap-southeast-1" && site == "international":
		host, action, commodity = "bailian-singapore-cs.alibabacloud.com", "IntlBroadScopeAspnGateway", "sfm_codingplan_public_intl"
	default:
		return empty, failure("needs_configuration", "unsupported_explicit_coding_plan_region_site")
	}
	if tokenFile == "" {
		return empty, failure("needs_configuration", "explicit_console_token_file_required")
	}
	file, err := os.Open(tokenFile)
	if err != nil {
		return empty, failure("needs_configuration", "cannot_read_explicit_console_token_file")
	}
	body, readErr := io.ReadAll(io.LimitReader(file, 65537))
	file.Close()
	token := strings.TrimSpace(string(body))
	// A raw, pre-enrolled token is required; a login/config JSON is not a token
	// or selected-key ownership evidence. Never search another file on failure.
	if readErr != nil || len(body) > 65536 || token == "" || strings.ContainsAny(token, " \t\r\n{}[]\"\\") {
		return empty, failure("needs_configuration", "invalid_explicit_console_token_file")
	}
	params, _ := json.Marshal(map[string]any{
		"Api": codingPlanAPI, "V": "1.0", "Data": map[string]any{
			"queryCodingPlanInstanceInfoRequest": map[string]any{"commodityCode": commodity, "onlyLatestOne": true},
			"cornerstoneParam":                   map[string]any{"protocol": "V2", "console": "ONE_CONSOLE", "productCode": "p_efm", "switchUserType": 3, "consoleSite": "BAILIAN_ALIYUN"},
		},
	})
	query := url.Values{"action": {action}, "product": {"sfm_bailian"}, "api": {codingPlanAPI}}
	form := url.Values{"params": {string(params)}, "region": {region}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/cli/api.json?"+query.Encode(), strings.NewReader(form.Encode()))
	if err != nil {
		return empty, failure("invalid_request", "cannot_assemble_coding_plan_query")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return empty, failure("timeout", "coding_plan_query_canceled_or_timed_out")
		}
		return empty, failure("network_error", "cannot_reach_coding_plan_gateway")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return empty, failure("not_logged_in", "console_token_unavailable_or_expired; no automatic login or refresh")
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return empty, failure("http_error", "coding_plan_gateway_failed_or_exceeded_response_limit")
	}
	return parseCodingPlan(body, c.Now())
}

func parseCodingPlan(body []byte, observed time.Time) (CodingPlanResult, error) {
	empty := CodingPlanResult{}
	data, err := rpcData(body)
	if err != nil {
		return empty, err
	}
	// The legacy wrapper decoder accepts some contradictory outer envelopes.
	// Coding Plan must fail closed on errors at every known gateway layer even
	// when a nested object also contains well-formed quota data.
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var node map[string]any
	_ = decoder.Decode(&node) // rpcData already checked JSON and trailing data.
	for _, child := range []string{"data", "DataV2", "data", "data"} {
		for _, key := range []string{"success", "successResponse"} {
			if value, present := node[key]; present && value != true {
				return empty, failure("rpc_error", "coding_plan_gateway_reported_failure")
			}
		}
		for _, key := range []string{"error", "errors", "errorCode"} {
			if value := node[key]; value != nil && value != "" {
				return empty, failure("rpc_error", "coding_plan_gateway_reported_error")
			}
		}
		if value, present := node["code"]; present && scalarText(value) != "200" && scalarText(value) != "SUCCESS" {
			return empty, failure("rpc_error", "coding_plan_gateway_reported_error_code")
		}
		node, _ = node[child].(map[string]any)
	}
	instances, ok := data["codingPlanInstanceInfos"].([]any)
	if !ok {
		return empty, failure("invalid_quota", "coding_plan_instance_list_missing")
	}
	var selected map[string]any
	for _, value := range instances {
		instance, _ := value.(map[string]any)
		if instance["status"] == "VALID" {
			selected = instance
			break
		}
	}
	if selected == nil {
		return empty, failure("needs_configuration", "no_valid_coding_plan_instance; account binding is unknown")
	}
	quota, ok := selected["codingPlanQuotaInfo"].(map[string]any)
	if !ok {
		return empty, failure("invalid_quota", "coding_plan_quota_info_missing")
	}
	instanceType, _ := selected["instanceType"].(string)
	return codingPlanReading(quota, instanceType, observed)
}

// Both transports use the same native-count/millisecond validation. Neither
// successful transport can assert selected-key ownership or account authority.
func codingPlanReading(quota map[string]any, instanceType string, observed time.Time) (CodingPlanResult, error) {
	empty := CodingPlanResult{}
	result := CodingPlanResult{Protocol: CodingPlanProtocol, Product: "coding-plan", QueryStatus: "success", AuthorityStatus: "needs_configuration", Reason: "account_authority_unverified", ObservedAt: observed.UTC().Format(time.RFC3339Nano), InstanceType: instanceType}
	for _, field := range []struct{ prefix, window string }{{"per5Hour", "5h"}, {"perWeek", "week"}, {"perBillMonth", "billing_month"}} {
		used, errUsed := nativeCount(quota[field.prefix+"UsedQuota"])
		total, errTotal := nativeCount(quota[field.prefix+"TotalQuota"])
		reset, okReset := quota[field.prefix+"QuotaNextRefreshTime"].(json.Number)
		millis, errReset := strconv.ParseInt(string(reset), 10, 64)
		if errUsed != nil || errTotal != nil || total.Sign() <= 0 || used.Cmp(total) > 0 || !okReset || errReset != nil || millis < 946684800000 || millis > 253402300799999 {
			return empty, failure("invalid_quota", "coding_plan_window_requires_exact_counts_and_millisecond_reset")
		}
		result.Windows = append(result.Windows, CodingPlanWindow{Window: field.window, Unit: "requests", Used: used.String(), Total: total.String(), Remaining: new(big.Int).Sub(total, used).String(), ResetTime: time.UnixMilli(millis).UTC().Format(time.RFC3339Nano)})
	}
	return result, nil
}

// Bound integer work before big.Int allocation. Reject strings, fractions,
// exponents and signs rather than silently rounding or treating null as zero.
func nativeCount(value any) (*big.Int, error) {
	number, ok := value.(json.Number)
	if !ok {
		return nil, fmt.Errorf("missing numeric count")
	}
	return exactCount(string(number))
}

func exactCount(value string) (*big.Int, error) {
	if len(value) == 0 || len(value) > 64 || len(value) > 1 && value[0] == '0' {
		return nil, fmt.Errorf("invalid count")
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return nil, fmt.Errorf("invalid count")
		}
	}
	integer, _ := new(big.Int).SetString(value, 10)
	return integer, nil
}

// DecodeCodingPlanResult validates the subprocess boundary independently. A
// helper cannot promote itself to account authority (nor add a verified flag).
func DecodeCodingPlanResult(body []byte) (CodingPlanResult, error) {
	empty := CodingPlanResult{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var result CodingPlanResult
	if len(body) > maxResponseBytes || decoder.Decode(&result) != nil {
		return empty, failure("needs_configuration", "coding_plan_helper_protocol_required")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || result.Protocol != CodingPlanProtocol || result.Product != "coding-plan" || result.QueryStatus != "success" || result.AuthorityStatus != "needs_configuration" || result.Reason != "account_authority_unverified" {
		return empty, failure("needs_configuration", "coding_plan_helper_protocol_required; account_authority_unverified")
	}
	if _, err := time.Parse(time.RFC3339Nano, result.ObservedAt); err != nil || len(result.Windows) != 3 {
		return empty, failure("invalid_quota", "coding_plan_helper_reading_incomplete")
	}
	for i, name := range []string{"5h", "week", "billing_month"} {
		window := result.Windows[i]
		used, errUsed := exactCount(window.Used)
		total, errTotal := exactCount(window.Total)
		remaining, errRemaining := exactCount(window.Remaining)
		reset, errReset := time.Parse(time.RFC3339Nano, window.ResetTime)
		if window.Window != name || window.Unit != "requests" || errUsed != nil || errTotal != nil || errRemaining != nil || total.Sign() <= 0 || used.Cmp(total) > 0 || new(big.Int).Sub(total, used).Cmp(remaining) != 0 || errReset != nil || reset.UnixMilli() < 946684800000 || reset.UnixMilli() > 253402300799999 || reset.Nanosecond()%int(time.Millisecond) != 0 {
			return empty, failure("invalid_quota", "coding_plan_helper_window_invalid")
		}
	}
	return result, nil
}
