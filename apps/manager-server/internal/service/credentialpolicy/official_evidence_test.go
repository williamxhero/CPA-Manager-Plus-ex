package credentialpolicy_test

import (
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
)

// These are exact VALUE classifications, never account/credential association or
// Core gate proofs. The consumer separately requires real association evidence.
func TestNativeKeyEvidenceSeparatesFiniteUnlimitedAndUnknown(t *testing.T) {
	finite, unlimited := false, true
	zero := "0"
	positive := "9007199254740993"
	negative := "-1"
	for _, tc := range []struct {
		name  string
		quota *balance.NewAPINativeQuota
		state string
	}{
		{"finite-zero", &balance.NewAPINativeQuota{Unit: "newapi_quota", State: "finite", Remaining: &zero, Unlimited: &finite}, "depleted"},
		{"finite-positive-exact", &balance.NewAPINativeQuota{Unit: "newapi_quota", State: "finite", Remaining: &positive, Unlimited: &finite}, "positive"},
		{"finite-negative", &balance.NewAPINativeQuota{Unit: "newapi_quota", State: "finite", Remaining: &negative, Unlimited: &finite}, "depleted"},
		{"unlimited-not-wallet", &balance.NewAPINativeQuota{Unit: "newapi_quota", State: "unlimited", Unlimited: &unlimited}, "unlimited"},
		{"unknown-not-zero", &balance.NewAPINativeQuota{Unit: "newapi_quota", State: "unknown"}, "unknown"},
		{"missing-remaining", &balance.NewAPINativeQuota{Unit: "newapi_quota", State: "finite", Unlimited: &finite}, "unknown"},
		{"missing-unlimited-flag", &balance.NewAPINativeQuota{Unit: "newapi_quota", State: "finite", Remaining: &zero}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := policy.ClassifyOfficialValue("newapi-token-usage-v1", balance.Observation{QueryStatus: "success", Semantic: "key_remaining", NativeQuota: tc.quota})
			if value.State != tc.state || value.Semantic != "key_remaining" || len(value.Currencies) != 0 {
				t.Fatalf("native key became money/incorrect zero: %+v", value)
			}
		})
	}
}

func TestMoneyValuesRemainExactPerCurrencyAndNeverDoubleCountGranted(t *testing.T) {
	obs := balance.Observation{QueryStatus: "success", Semantic: "account_wallet", Balances: []balance.CurrencyBalance{
		{Currency: "USD", Total: "-0.000000000000000000000000000000000000000001", Granted: "100", ToppedUp: "100"},
		{Currency: "CNY", Total: "9007199254740993.01", Granted: "200", ToppedUp: "300"},
	}}
	value := policy.ClassifyOfficialValue("deepseek", obs)
	if value.State != "multiple_currencies" || len(value.Currencies) != 2 || value.Currencies[0].Sign != -1 || value.Currencies[1].Sign != 1 || value.Currencies[1].Total != "9007199254740993.01" {
		t.Fatalf("currency summed/rounded or totals double counted: %+v", value)
	}
	obs.Balances = obs.Balances[1:]
	value = policy.ClassifyOfficialValue("deepseek", obs)
	if value.State != "positive" || value.Currencies[0].Total != "9007199254740993.01" {
		t.Fatalf("granted/topped up added to authoritative total: %+v", value)
	}
}

func TestSignedPrepaidNetAndInvalidNumbersAreNotUniversalSpendableProof(t *testing.T) {
	for _, value := range []string{"0", "-1", "999999"} {
		classified := policy.ClassifyOfficialValue("deepinfra", balance.Observation{QueryStatus: "success", Semantic: "signed_prepaid_net", Currency: "USD", AmountDecimal: value})
		if classified.State != "unknown" {
			t.Fatal("signed prepaid net inferred universal depletion/recovery")
		}
	}
	for _, value := range []string{"", "null", "NaN", "Infinity", "1/2", "1e999", "+1"} {
		classified := policy.ClassifyOfficialValue("deepseek", balance.Observation{QueryStatus: "success", Semantic: "account_wallet", Currency: "USD", AmountDecimal: value})
		if classified.State != "unknown" {
			t.Fatalf("invalid decimal accepted: %q", value)
		}
	}
}
