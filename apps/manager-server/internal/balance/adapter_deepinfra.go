package balance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// First-party billing client: GET /payment/checklist -> -(stripe_balance+recent)
// displayed directly as USD Prepaid Credits. Not a universal spendable wallet,
// grants total, or a claim about recent's settlement interval. See the parent
// evidence deepinfra-currency-research.md (2026-11-01 bounded source trace).
func queryDeepInfra(ctx context.Context, t QueryTarget, credential string, n NetworkOptions, out Observation) Observation {
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

	data, valid := checklistNumbers(body)
	if !valid {
		return fail("parse_error", "INVALID_BALANCE")
	}
	amount, ok := signedPrepaid(data["stripe_balance"], data["recent"])
	if !ok {
		return fail("parse_error", "INVALID_BALANCE")
	}
	out.AmountDecimal = amount
	out.Currency = "USD"
	out.Semantic = "signed_prepaid_net"
	out.QueryStatus = "success"
	out.ErrorCode = ""
	return out
}

// Decode original numeric tokens, rejecting duplicate top-level fields instead
// of silently choosing the last financial value. Unknown fields (limit, grants,
// suspension) are deliberately not money inputs. Whole-body syntax must be JSON.
func checklistNumbers(body []byte) (map[string]json.RawMessage, bool) {
	d := json.NewDecoder(bytes.NewReader(body))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, false
		}
		key, ok := token.(string)
		if !ok {
			return nil, false
		}
		if _, exists := fields[key]; exists {
			return nil, false
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, false
		}
		fields[key] = raw
	}
	if _, err = d.Token(); err != nil {
		return nil, false
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false
	}
	return fields, true
}
