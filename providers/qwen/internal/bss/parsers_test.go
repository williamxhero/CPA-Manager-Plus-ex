package bss

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func envelope(data string) []byte {
	return []byte(`{"Code":"200","Success":true,"Data":` + data + `}`)
}

func TestBalanceRealisticResponse(t *testing.T) {
	result, err := ParseBalance(envelope(`{"AvailableAmount":"10000.00","AvailableCashAmount":"123.45","CreditAmount":"0.00","Currency":"CNY","QuotaLimit":"99999"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.AvailableAmount == nil || *result.AvailableAmount != 10000 || result.AvailableCashAmount == nil || *result.AvailableCashAmount != 123.45 || result.CreditAmount == nil || *result.CreditAmount != 0 || result.Currency != "CNY" {
		t.Fatalf("%+v", result)
	}
}

func TestBalanceMissingNumbersOmitted(t *testing.T) {
	result, err := ParseBalance(envelope(`{"AvailableAmount":0,"Currency":"USD"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.AvailableAmount == nil || *result.AvailableAmount != 0 || result.AvailableCashAmount != nil || result.CreditAmount != nil {
		t.Fatalf("%+v", result)
	}
}

func TestBalanceCurrencyNotInvented(t *testing.T) {
	result, err := ParseBalance(envelope(`{"AvailableAmount":"-3.2"}`))
	if err != nil || result.Currency != "" || *result.AvailableAmount != -3.2 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestBalanceNoAmountsIsError(t *testing.T) {
	for _, data := range []string{`{}`, `{"Currency":"CNY","QuotaLimit":"100"}`, `{"AvailableAmount":null}`, `{"AvailableAmount":""}`} {
		if _, err := ParseBalance(envelope(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestBalanceInvalidAmounts(t *testing.T) {
	for _, value := range []string{`"NaN"`, `"Inf"`, `"oops"`, `true`, `{}`, `[]`, `"1,2"`, `"1.2,34"`, `"1,23e4"`, `"1e309"`} {
		if _, err := ParseBalance(envelope(`{"AvailableAmount":` + value + `}`)); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestBalanceFormattedNumbers(t *testing.T) {
	result, err := ParseBalance(envelope(`{"AvailableAmount":"1,234.56","CreditAmount":123}`))
	if err != nil || *result.AvailableAmount != 1234.56 || *result.CreditAmount != 123 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestEnvelopeRequiresSuccessAndCode(t *testing.T) {
	for _, raw := range []string{
		`{"Code":"200","Success":false,"Data":{"AvailableAmount":1}}`,
		`{"Code":"200","Data":{"AvailableAmount":1}}`,
		`{"Code":"Other","Success":true,"Data":{"AvailableAmount":1}}`,
		`{"Success":true,"Data":{"AvailableAmount":1}}`,
		`{"Code":"200","Success":"true","Data":{"AvailableAmount":1}}`,
	} {
		if _, err := ParseBalance([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	result, err := ParseBalance([]byte(`{"Code":200,"Success":true,"Data":{"AvailableAmount":1}}`))
	if err != nil || *result.AvailableAmount != 1 {
		t.Fatal(result, err)
	}
}

func TestEnvelopeRequiresDataObject(t *testing.T) {
	for _, raw := range []string{`{"Code":"200","Success":true}`, string(envelope(`null`)), string(envelope(`[]`)), string(envelope(`"bad"`))} {
		if _, err := ParseBalance([]byte(raw)); err == nil {
			t.Fatal("accepted invalid data")
		}
	}
}

func TestEnvelopeInvalidJSONAndSize(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{`, string(envelope(`{}`)) + ` {}`, strings.Repeat("x", MaxResponseBytes+1)} {
		if _, err := ParseBalance([]byte(raw)); err == nil {
			t.Fatal("accepted invalid JSON or oversized body")
		}
	}
}

func TestAPIErrorHasCodeAndSafeMessage(t *testing.T) {
	_, err := ParseBalance([]byte(`{"Code":"InvalidAccessKeyId.NotFound","Success":false,"Message":"not found"}`))
	if err == nil || !strings.Contains(err.Error(), "InvalidAccessKeyId.NotFound") || !strings.Contains(err.Error(), "not found") {
		t.Fatal(err)
	}
}

func TestAPIErrorDoesNotEchoQuery(t *testing.T) {
	_, err := ParseBalance([]byte(`{"Code":"Invalid","Message":"AccessKeyId=fixture&Signature=signed-value"}`))
	if err == nil || strings.Contains(err.Error(), "fixture") || strings.Contains(err.Error(), "signed-value") {
		t.Fatal(err)
	}
}

func TestBillPretaxPreferredAndPaymentFallback(t *testing.T) {
	result, err := ParseBill(envelope(`{"Items":{"Item":[{"PretaxAmount":"2.5","PaymentAmount":"100","Currency":"CNY"},{"PaymentAmount":3},{"PretaxAmount":0,"PaymentAmount":10},{"PretaxAmount":null,"PaymentAmount":"1"}]}}`))
	if err != nil || result.Amount == nil || *result.Amount != 6.5 || result.Currency != "CNY" {
		t.Fatal(result, err)
	}
}

func TestBillMissingRowsNoFabricatedZero(t *testing.T) {
	for _, data := range []string{`{}`, `{"Items":{"Item":[]}}`, `{"Items":null}`, `{"Items":{"Item":null}}`} {
		result, err := ParseBill(envelope(data))
		if err != nil || result.Amount != nil {
			t.Fatal(result, err)
		}
	}
}

func TestBillInvalidOrMissingItemAmount(t *testing.T) {
	for _, data := range []string{`{"Items":{"Item":[{}]}}`, `{"Items":{"Item":[{"PretaxAmount":"bad","PaymentAmount":2}]}}`, `{"Items":{"Item":[{"PaymentAmount":"NaN"}]}}`} {
		if _, err := ParseBill(envelope(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestBillMixedCurrenciesRejected(t *testing.T) {
	if _, err := ParseBill(envelope(`{"Items":{"Item":[{"PretaxAmount":1,"Currency":"CNY"},{"PretaxAmount":2,"Currency":"USD"}]}}`)); err == nil {
		t.Fatal("mixed currencies summed")
	}
}

func TestBillMalformedAndIncompleteCollections(t *testing.T) {
	for _, data := range []string{`{"Items":{}}`, `{"Items":{"Item":{}}}`, `{"Items":[null]}`, `{"TotalCount":2,"Items":{"Item":[{"PretaxAmount":1}]}}`, `{"TotalCount":1}`, `{"NextToken":"next","Items":[]}`} {
		if _, err := ParseBill(envelope(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestBillDirectArrayAndZero(t *testing.T) {
	result, err := ParseBill(envelope(`{"Items":[{"PretaxAmount":"0"}],"Currency":"CNY","TotalCount":1}`))
	if err != nil || result.Amount == nil || *result.Amount != 0 {
		t.Fatal(result, err)
	}
}

func TestBillTotalOverflow(t *testing.T) {
	if _, err := ParseBill(envelope(`{"Items":[{"PretaxAmount":1e308},{"PretaxAmount":1e308}]}`)); err == nil {
		t.Fatal("overflow accepted")
	}
}

func TestPackagesRealisticInstances(t *testing.T) {
	result, err := ParsePackages(envelope(`{"TotalCount":1,"Instances":{"Instance":[{"InstanceId":"fixture-id","PackageName":"tokens","CommodityCode":"bailian_package","RemainingAmount":"25","TotalAmount":"100","Unit":"tokens","ExpiryTime":"2026-11-01T00:00:00Z"}]}}`))
	if err != nil || len(result) != 1 {
		t.Fatal(result, err)
	}
	item := result[0]
	if item.Name != "tokens" || item.ID != "fixture-id" || item.RemainingFraction != .25 || item.Unit != "tokens" || item.ExpiresAt == nil || !item.ExpiresAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", item)
	}
}

func TestPackagesApplicableProductsAndProductNames(t *testing.T) {
	for _, field := range []string{`"ApplicableProducts":{"Product":["bailian"]}`, `"ProductName":"Model Studio"`, `"ProductName":"百炼"`, `"ProductCode":"modelstudio"`} {
		result, err := ParsePackages(envelope(`{"Items":{"Item":[{` + field + `,"InstanceId":"fixture","RemainingAmount":1,"TotalAmount":2}]}}`))
		if err != nil || len(result) != 1 || result[0].Name != "fixture" {
			t.Fatal(result, err)
		}
	}
}

func TestPackagesFilterUnrelatedAndInvalidAmounts(t *testing.T) {
	for _, item := range []string{
		`{"ProductName":"ECS","PackageName":"x","RemainingAmount":1,"TotalAmount":2}`,
		`{"ProductName":"百炼","PackageName":"x","RemainingAmount":1,"TotalAmount":0}`,
		`{"ProductName":"百炼","PackageName":"x","RemainingAmount":-1,"TotalAmount":2}`,
		`{"ProductName":"百炼","PackageName":"x","RemainingAmount":3,"TotalAmount":2}`,
		`{"ProductName":"百炼","PackageName":"x","TotalAmount":2}`,
		`{"ProductName":"百炼","PackageName":"x","RemainingAmount":"NaN","TotalAmount":2}`,
		`{"ProductName":"百炼","RemainingAmount":1,"TotalAmount":2}`,
	} {
		result, err := ParsePackages(envelope(`{"Instances":{"Instance":[` + item + `]}}`))
		if err != nil || len(result) != 0 {
			t.Fatal(result, err)
		}
	}
}

func TestPackagesZeroRemainingAndInvalidExpiry(t *testing.T) {
	result, err := ParsePackages(envelope(`{"Items":[{"ProductName":"百炼","InstanceName":"depleted","RemainingAmount":0,"TotalAmount":2,"ExpiryTime":"unknown"}]}`))
	if err != nil || len(result) != 1 || result[0].RemainingFraction != 0 || result[0].ExpiresAt != nil {
		t.Fatal(result, err)
	}
}

func TestPackageExpirationFormats(t *testing.T) {
	for _, value := range []string{"2026-10-01T12:00:00+08:00", "2026-10-01T12:00:00+0800", "2026-10-01 12:00:00", "2026-10-01", "2026-10-01T12:00:00.123Z"} {
		body := envelope(`{"Items":[{"ProductCode":"bailian","PackageName":"x","RemainingAmount":1,"TotalAmount":1,"ExpirationTime":` + strconvJSON(value) + `}]}`)
		result, err := ParsePackages(body)
		if err != nil || result[0].ExpiresAt == nil {
			t.Fatal(result, err)
		}
	}
}

func strconvJSON(value string) string { raw, _ := json.Marshal(value); return string(raw) }

func TestPackagesMissingAndIncomplete(t *testing.T) {
	result, err := ParsePackages(envelope(`{}`))
	if err != nil || len(result) != 0 {
		t.Fatal(result, err)
	}
	if _, err := ParsePackages(envelope(`{"TotalCount":3,"Instances":{"Instance":[]}}`)); err == nil {
		t.Fatal("partial packages accepted")
	}
	if _, err := ParsePackages([]byte(`{"Code":"Denied","Success":false}`)); err == nil {
		t.Fatal("API failure accepted")
	}
}
