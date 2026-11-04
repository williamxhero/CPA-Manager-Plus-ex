package balance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// OpenAICompatUsageV1 is the conter_proj openai_new_api seam for hosts that
// expose a monetary wallet on GET /v1/usage (e.g. WawAPI). Exact decimal USD
// account_wallet only — never a NewAPI quota divisor or UI ×7 display rate.
const OpenAICompatUsageV1 = "openai-compat-usage-v1"
const OpenAICompatUsagePath = "/v1/usage"

func queryOpenAICompatUsage(ctx context.Context, t QueryTarget, credential string, n NetworkOptions, out Observation) Observation {
	fail := func(status, code string) Observation { out.QueryStatus = status; out.ErrorCode = code; return out }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.EndpointURL, nil)
	if err != nil {
		return fail("network_error", "INVALID_ENDPOINT")
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Accept", "application/json")
	client := secureClient(t, n)
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		return fail("network_error", "UPSTREAM_UNAVAILABLE")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return fail("auth_error", "UPSTREAM_AUTH")
	}
	if res.StatusCode == 429 {
		return fail("rate_limited", "UPSTREAM_RATE_LIMITED")
	}
	if res.StatusCode != 200 {
		return fail("network_error", "UPSTREAM_STATUS")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBodySize+1))
	if err != nil {
		return fail("network_error", "RESPONSE_READ_FAILED")
	}
	if len(body) > maxResponseBodySize {
		return fail("parse_error", "RESPONSE_TOO_LARGE")
	}
	amount, ok := parseCompatUsageBalance(body)
	if !ok {
		return fail("parse_error", "INVALID_BALANCE")
	}
	out.AmountDecimal = amount
	out.Currency = "USD"
	out.Semantic = "account_wallet"
	out.QueryStatus = "success"
	out.ErrorCode = ""
	return out
}

// Accept exactly one balance field: top-level `balance` or nested `data.balance`.
// Reject both present, duplicates, non-finite tokens, HTML, and trailing junk.
func parseCompatUsageBalance(body []byte) (string, bool) {
	fields, ok := checklistNumbers(body)
	if !ok {
		return "", false
	}
	top, hasTop := fields["balance"]
	dataRaw, hasData := fields["data"]
	if hasTop && hasData {
		nested, nestedOK := objectFields(dataRaw)
		if nestedOK {
			if _, hasNested := nested["balance"]; hasNested {
				return "", false
			}
		}
		return exactMoneyToken(top)
	}
	if hasTop {
		return exactMoneyToken(top)
	}
	if !hasData {
		return "", false
	}
	nested, nestedOK := objectFields(dataRaw)
	if !nestedOK {
		return "", false
	}
	nestedBal, hasNested := nested["balance"]
	if !hasNested {
		return "", false
	}
	return exactMoneyToken(nestedBal)
}

func objectFields(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	return checklistNumbers(raw)
}

func exactMoneyToken(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil || !money.MatchString(s) {
			return "", false
		}
		return s, true
	}
	n, scale, ok := numericDecimal(raw)
	if !ok {
		return "", false
	}
	return formatExactDecimal(n, scale), true
}
