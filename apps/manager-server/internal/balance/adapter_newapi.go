package balance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	NewAPIUserSelfV1             = "newapi-user-self-v1"
	NewAPIUserPATWithIDV1        = "user-pat-with-user-id-v1"
	NewAPIUserPATV1              = "user-pat-v1"
	NewAPITokenUsageV1           = "newapi-token-usage-v1"
	NewAPIInferenceKeyReadOnlyV1 = "inference-key-readonly-v1"
)

// NewAPIConfig is an explicit administrator-selected instance contract, not a
// hostname heuristic. InstanceVersion records operator confirmation, not remote
// version discovery. No session authentication or unit conversion is implied.
type NewAPIConfig struct {
	Conversion        *NewAPIConversionProfile `json:"conversion,omitempty"`
	AccountScope      string                   `json:"accountScope,omitempty"`
	NativeUnit        string                   `json:"nativeUnit"`
	ProtocolConfirmed bool                     `json:"protocolConfirmed"`
	InstanceVersion   string                   `json:"instanceVersion"`
	Auth              string                   `json:"auth"`
	UserID            string                   `json:"userID,omitempty"`
}

// NewAPINativeQuota preserves native integer units independently of money.
// Nil Remaining is not zero and must never be used as exhaustion evidence.
type NewAPINativeQuota struct {
	Unit      string  `json:"unit"`
	State     string  `json:"state"`
	Remaining *string `json:"remaining,omitempty"`
	Unlimited *bool   `json:"unlimited,omitempty"`
}

var newAPIInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,119})$`)
var newAPIUserID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var newAPIVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+@ -]{0,127}$`)

func newAPIInt(raw json.RawMessage) (string, bool) {
	value := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return "", false
		}
	}
	if !newAPIInteger.MatchString(value) {
		return "", false
	}
	if value == "-0" {
		value = "0"
	}
	return value, true
}

// QueryNewAPI uses the SAME pinned, whole-answer checked, certificate-validating
// transport as the other balance adapters. It never owns a client or scheduler.
// Native quota remains independent; only an explicit validated profile enables
// exact money conversion. There is never an inferred unit/currency/rate.
func QueryNewAPI(ctx context.Context, t QueryTarget, c NewAPIConfig, resolver CredentialResolver, n NetworkOptions) Observation {
	out := Observation{QueryID: uuid.NewString(), ObservedAtMS: time.Now().UnixMilli(), CapabilityStatus: "needs_configuration", QueryStatus: "needs_configuration", ErrorCode: "NEWAPI_CONTRACT_REQUIRED"}
	fail := func(status, code string) Observation {
		out.QueryStatus = status
		out.ErrorCode = code
		if status == "unsupported" || status == "needs_configuration" {
			out.CapabilityStatus = status
		}
		return out
	}
	if c.AccountScope != "" {
		if t.AccountScope != "" && t.AccountScope != c.AccountScope {
			return fail("needs_configuration", "NEWAPI_ACCOUNT_SCOPE_MISMATCH")
		}
		t.AccountScope = c.AccountScope
	}
	if !validNewAPIConversion(c.Conversion, c.NativeUnit) {
		return fail("needs_configuration", "INVALID_CONVERSION_PROFILE")
	}
	// Do not silently accept requested money units in the native-only contract.
	if t.Currency != "" || t.UnitMultiplier != "" {
		return fail("needs_configuration", "NEWAPI_UNIT_PROOF_REQUIRED")
	}
	if !c.ProtocolConfirmed || strings.TrimSpace(c.InstanceVersion) == "" || len(c.InstanceVersion) > 128 {
		return out
	}
	switch t.Adapter {
	case NewAPIUserSelfV1:
		if (c.Auth != NewAPIUserPATWithIDV1 && c.Auth != NewAPIUserPATV1) || !newAPIUserID.MatchString(c.UserID) || strings.TrimSpace(t.AccountScope) == "" {
			return out
		}
	case NewAPITokenUsageV1:
		if c.Auth != NewAPIInferenceKeyReadOnlyV1 || c.UserID != "" {
			return out
		}
	default:
		return fail("unsupported", "ADAPTER_NOT_FOUND")
	}
	if len(t.PrivateTargets) > 16 || ValidateAdapterSource(t.Adapter, t.EndpointURL, t.PrivateTargets) != nil {
		return fail("needs_configuration", "INVALID_ENDPOINT")
	}
	if resolver == nil {
		return fail("needs_configuration", "CREDENTIAL_MISSING")
	}
	credential, err := resolver.ResolveCredential(ctx, t.CredentialRef)
	if err != nil {
		return fail("auth_error", "CREDENTIAL_RESOLVE_FAILED")
	}
	if strings.TrimSpace(credential) == "" {
		return fail("needs_configuration", "CREDENTIAL_MISSING")
	}
	out.CapabilityStatus = "supported"
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	if timeout > maxQueryTimeout {
		timeout = maxQueryTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.EndpointURL, nil)
	if err != nil {
		return fail("network_error", "INVALID_ENDPOINT")
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	if t.Adapter == NewAPIUserSelfV1 && c.Auth == NewAPIUserPATWithIDV1 {
		req.Header.Set("New-Api-User", c.UserID)
	}
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
	if !newAPIUnambiguousJSON(body) {
		return fail("parse_error", "INVALID_NEWAPI_RESPONSE")
	}
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Success *bool           `json:"success"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Error) > 0 && string(envelope.Error) != "null" || envelope.Success != nil && !*envelope.Success {
		return fail("parse_error", "INVALID_NEWAPI_RESPONSE")
	}
	if t.Adapter == NewAPITokenUsageV1 {
		var token struct {
			Code *bool `json:"code"`
			Data struct {
				Object    string          `json:"object"`
				Remaining json.RawMessage `json:"total_available"`
				Unlimited *bool           `json:"unlimited_quota"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &token) != nil || token.Code == nil || !*token.Code || token.Data.Object != "token_usage" {
			return fail("parse_error", "INVALID_NEWAPI_RESPONSE")
		}
		q := &NewAPINativeQuota{Unit: "newapi_quota", State: "unknown", Unlimited: token.Data.Unlimited}
		if token.Data.Unlimited != nil {
			if *token.Data.Unlimited {
				q.State = "unlimited"
			} else {
				quota, ok := newAPIInt(token.Data.Remaining)
				if !ok {
					return fail("parse_error", "INVALID_NATIVE_QUOTA")
				}
				q.State = "finite"
				q.Remaining = &quota
			}
		}
		out.NativeQuota = q
		out.Semantic = "key_remaining"
		out.QueryStatus = "success"
		out.ErrorCode = ""
		return applyNewAPIConversion(out, c.Conversion)
	}
	var data struct {
		Success *bool `json:"success"`
		Data    struct {
			ID    json.RawMessage `json:"id"`
			Quota json.RawMessage `json:"quota"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &data) != nil || data.Success == nil || !*data.Success {
		return fail("parse_error", "INVALID_NEWAPI_RESPONSE")
	}
	id, ok := newAPIInt(data.Data.ID)
	if !ok || id != c.UserID {
		return fail("parse_error", "NEWAPI_ACCOUNT_MISMATCH")
	}
	quota, ok := newAPIInt(data.Data.Quota)
	if !ok {
		return fail("parse_error", "INVALID_NATIVE_QUOTA")
	}
	out.NativeQuota = &NewAPINativeQuota{Unit: "newapi_quota", State: "finite", Remaining: &quota}
	out.Semantic = "account_wallet"
	out.QueryStatus = "success"
	out.ErrorCode = ""
	return applyNewAPIConversion(out, c.Conversion)
}

// Duplicate keys, including case-fold aliases accepted by encoding/json's
// struct decoder, cannot silently replace an amount or authentication result.
// Bound nesting as well as bytes; unknown fields are checked but never emitted.
func newAPIUnambiguousJSON(body []byte) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 64 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delim, composite := token.(json.Delim)
		if !composite {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				name = newAPIKeyFold(name)
				if !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}

// encoding/json matches struct field aliases using Unicode simple folding,
// not strings.ToLower (e.g. long s U+017F also matches ASCII S). Select the
// smallest rune in each SimpleFold orbit, just as encoding/json's foldRune.
func newAPIKeyFold(name string) string {
	return strings.Map(func(r rune) rune {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		return minimum
	}, name)
}

// ValidateNewAPIContract is the shared persisted-config authority contract.
// Native quota is a protocol unit, never an implied monetary denomination.
func ValidateNewAPIContract(adapter string, c *NewAPIConfig) error {
	bad := errors.New("invalid NewAPI contract")
	if c == nil || !c.ProtocolConfirmed || !newAPIVersion.MatchString(c.InstanceVersion) || c.NativeUnit != "newapi_quota" || len(c.AccountScope) > 256 || strings.ContainsAny(c.AccountScope, "\r\n\t") {
		return bad
	}
	switch adapter {
	case NewAPIUserSelfV1:
		if strings.TrimSpace(c.AccountScope) == "" || !newAPIUserID.MatchString(c.UserID) || c.Auth != NewAPIUserPATWithIDV1 && c.Auth != NewAPIUserPATV1 {
			return bad
		}
	case NewAPITokenUsageV1:
		if c.UserID != "" || c.Auth != NewAPIInferenceKeyReadOnlyV1 {
			return bad
		}
	case NewAPIBrowserSessionV1:
		if strings.TrimSpace(c.AccountScope) == "" || !newAPIUserID.MatchString(c.UserID) || c.Auth != NewAPIBrowserSessionAuthV1 {
			return bad
		}
	default:
		return bad
	}
	if !validNewAPIConversion(c.Conversion, c.NativeUnit) {
		return bad
	}
	return nil
}
