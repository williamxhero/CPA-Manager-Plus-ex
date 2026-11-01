package credentialpolicy

import (
	"math/big"
	"regexp"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

type CurrencyClassification struct {
	Currency string `json:"currency"`
	Total    string `json:"total"`
	Sign     int    `json:"sign"`
}

// ValueClassification deliberately carries no gate action. Exact arithmetic is
// not evidence that a query secret/account belongs to an inference credential.
type ValueClassification struct {
	Semantic        string                   `json:"semantic"`
	State           string                   `json:"state"`
	Reason          string                   `json:"reason"`
	Unit            string                   `json:"unit,omitempty"`
	NativeRemaining string                   `json:"nativeRemaining,omitempty"`
	Currencies      []CurrencyClassification `json:"currencies"`
}

var nativeInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,119})$`)
var policyMoney = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,119})(\.[0-9]{1,120})?$`)
var policyCurrency = regexp.MustCompile(`^[A-Z]{3}$`)

func ClassifyOfficialValue(adapter string, obs balance.Observation) ValueClassification {
	out := ValueClassification{Semantic: obs.Semantic, State: "unknown", Reason: "authoritative_value_required", Currencies: []CurrencyClassification{}}
	if obs.QueryStatus != "success" || obs.ErrorCode != "" {
		return out
	}
	if obs.NativeQuota != nil {
		quota := obs.NativeQuota
		out.Unit = quota.Unit
		if quota.Unit != "newapi_quota" || obs.Semantic != "key_remaining" && obs.Semantic != "account_wallet" {
			return out
		}
		if quota.State == "unlimited" && obs.Semantic == "key_remaining" && quota.Unlimited != nil && *quota.Unlimited && quota.Remaining == nil {
			out.State = "unlimited"
			out.Reason = "native_key_unlimited"
			return out
		}
		if quota.State != "finite" || quota.Remaining == nil || !nativeInteger.MatchString(*quota.Remaining) || obs.Semantic == "key_remaining" && (quota.Unlimited == nil || *quota.Unlimited) {
			return out
		}
		value, ok := new(big.Int).SetString(*quota.Remaining, 10)
		if !ok {
			return out
		}
		out.NativeRemaining = value.String()
		out.State = "depleted"
		if value.Sign() > 0 {
			out.State = "positive"
		}
		out.Reason = "native_units_not_money"
		return out
	}
	if adapter == "deepinfra" || obs.Semantic == "signed_prepaid_net" {
		out.Reason = "spendable_scope_unverified"
		return out
	}
	if obs.Semantic != "account_wallet" {
		return out
	}
	values := obs.Balances
	if len(values) == 0 && obs.AmountDecimal != "" {
		values = []balance.CurrencyBalance{{Currency: obs.Currency, Total: obs.AmountDecimal}}
	}
	if len(values) == 0 || len(values) > 32 {
		return out
	}
	classified := []CurrencyClassification{}
	seen := map[string]bool{}
	for _, currency := range values {
		if !policyCurrency.MatchString(currency.Currency) || seen[currency.Currency] || !policyMoney.MatchString(currency.Total) {
			return out
		}
		amount, ok := new(big.Rat).SetString(currency.Total)
		if !ok {
			return out
		}
		seen[currency.Currency] = true
		classified = append(classified, CurrencyClassification{Currency: currency.Currency, Total: currency.Total, Sign: amount.Sign()})
	}
	out.Currencies = classified
	out.Reason = "credential_account_association_required"
	out.State = "multiple_currencies"
	if len(classified) == 1 {
		out.State = "depleted"
		if classified[0].Sign > 0 {
			out.State = "positive"
		}
	}
	return out
}
