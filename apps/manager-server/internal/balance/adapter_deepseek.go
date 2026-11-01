package balance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
)

// No exponent coercion, float conversion, rounding or missing-as-zero. Both
// integer and fractional digits are bounded to prevent resource exhaustion.
var money = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,59})(\.[0-9]{1,30})?$`)
var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

func queryDeepSeek(ctx context.Context, t QueryTarget, credential string, n NetworkOptions, out Observation) Observation {
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
	var data struct {
		Available *bool `json:"is_available"`
		Balances  []struct {
			Currency string  `json:"currency"`
			Total    *string `json:"total_balance"`
			Granted  *string `json:"granted_balance"`
			ToppedUp *string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if json.Unmarshal(body, &data) != nil || data.Available == nil || len(data.Balances) == 0 || len(data.Balances) > 32 {
		return fail("parse_error", "INVALID_BALANCE")
	}
	values := make([]CurrencyBalance, 0, len(data.Balances))
	seen := map[string]bool{}
	for _, b := range data.Balances {
		if !currencyCode.MatchString(b.Currency) || seen[b.Currency] || b.Total == nil || b.Granted == nil || b.ToppedUp == nil || !money.MatchString(*b.Total) || !money.MatchString(*b.Granted) || !money.MatchString(*b.ToppedUp) {
			return fail("parse_error", "INVALID_BALANCE")
		}
		seen[b.Currency] = true
		values = append(values, CurrencyBalance{b.Currency, *b.Total, *b.Granted, *b.ToppedUp})
	}
	out.Available = data.Available
	out.Balances = values
	out.Semantic = "account_wallet"
	out.QueryStatus = "success"
	out.ErrorCode = ""
	return out
}
