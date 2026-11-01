package balance

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"regexp"
	"strings"
)

// Optional means absent, not explicit null. Keep unknown/duplicate financial
// config keys fail-closed even inside the enclosing HTTP decoder or SQLite JSON.
func (c *NewAPIConfig) UnmarshalJSON(raw []byte) error {
	type plain NewAPIConfig
	var value plain
	bad := errors.New("invalid NewAPI configuration")
	if !newAPIUnambiguousJSON(raw) {
		return bad
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return bad
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return bad
	}
	for name, field := range fields {
		if newAPIKeyFold(name) == newAPIKeyFold("conversion") && bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
			return bad
		}
	}
	*c = NewAPIConfig(value)
	return nil
}

// Rate direction is currency MAJOR units per ONE native quota unit, never its
// inverse. Every field is explicit; configuration is an operator assertion,
// not independent verification of any deployed account or unit denomination.
type NewAPIConversionProfile struct {
	Confirmed           bool   `json:"confirmed"`
	NativeUnit          string `json:"nativeUnit"`
	Currency            string `json:"currency"`
	MoneyPerNativeQuota string `json:"moneyPerNativeQuota"`
	ProofReference      string `json:"proofReference"`
	ContractVersion     string `json:"contractVersion"`
}

type NewAPIConversionEvidence struct {
	NewAPIConversionProfile
	Qualification string `json:"qualification"`
}

var newAPIRate = regexp.MustCompile(`^(0|[1-9][0-9]{0,119})(\.[0-9]{1,120})?$`)
var newAPIProof = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validNewAPIConversion(p *NewAPIConversionProfile, unit string) bool {
	if p == nil {
		return true
	}
	if !p.Confirmed || p.NativeUnit != "newapi_quota" || p.NativeUnit != unit || !currencyCode.MatchString(p.Currency) || !newAPIRate.MatchString(p.MoneyPerNativeQuota) || !newAPIProof.MatchString(p.ProofReference) || !newAPIVersion.MatchString(p.ContractVersion) {
		return false
	}
	n, _, ok := numericDecimal(json.RawMessage(p.MoneyPerNativeQuota))
	return ok && n.Sign() > 0
}

func applyNewAPIConversion(out Observation, p *NewAPIConversionProfile) Observation {
	if p == nil {
		return out
	}
	out.Conversion = &NewAPIConversionEvidence{NewAPIConversionProfile: *p, Qualification: "operator_asserted_contract_not_live_verified"}
	// Unlimited/unknown counters, including finite-looking upstream sentinels,
	// can never become finite money. The independently typed native state stays.
	if out.NativeQuota == nil || out.NativeQuota.State != "finite" || out.NativeQuota.Remaining == nil {
		return out
	}
	n, ok := new(big.Int).SetString(*out.NativeQuota.Remaining, 10)
	rate, scale, valid := numericDecimal(json.RawMessage(p.MoneyPerNativeQuota))
	if !ok || !valid {
		out.QueryStatus = "parse_error"
		out.ErrorCode = "INVALID_CONVERSION_PROFILE"
		return out
	}
	product := new(big.Int).Mul(n, rate)
	amount := formatExactDecimal(product, scale)
	parts := strings.SplitN(strings.TrimPrefix(amount, "-"), ".", 2)
	// Protocol quota and rate mantissas are each <=120 digits. Products are
	// computed exactly, then bounded to120 integer/fraction digits, never rounded.
	if len(parts[0]) > 120 || len(parts) > 1 && len(parts[1]) > 120 {
		out.QueryStatus = "parse_error"
		out.ErrorCode = "CONVERSION_PRECISION_EXCEEDED"
		return out
	}
	out.AmountDecimal = amount
	out.Currency = p.Currency
	return out
}
