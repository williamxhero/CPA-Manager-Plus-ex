package bss_test

import (
	"strings"
	"testing"

	"qwen-cliproxyapi/internal/bss"
)

func success(data string) []byte {
	return []byte(`{"Code":"200","Success":true,"Data":` + data + `}`)
}

func TestBalanceMissingNullAndRealZero(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"missing", `{}`, false},
		{"null", `{"AvailableAmount":null,"AvailableCashAmount":null,"CreditAmount":null}`, false},
		{"empty", `{"AvailableAmount":""}`, false},
		{"numeric-zero", `{"AvailableAmount":0,"AvailableCashAmount":null}`, true},
		{"string-zero", `{"AvailableAmount":"0.00"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bss.ParseBalance(success(tc.data))
			if !tc.valid {
				if err == nil {
					t.Fatal("required balance amount missing but accepted")
				}
				return
			}
			if err != nil || got.AvailableAmount == nil || *got.AvailableAmount != 0 || got.AvailableCashAmount != nil || got.CreditAmount != nil {
				t.Fatalf("zero/missing distinction lost: %+v, %v", got, err)
			}
		})
	}
}

func TestBillMissingNullAndRealZero(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		present    bool
	}{
		{"missing", `{}`, false},
		{"null-collection", `{"Items":null}`, false},
		{"empty", `{"Items":{"Item":[]},"TotalCount":0}`, false},
		{"real-zero", `{"Items":[{"PretaxAmount":0}]}`, true},
		{"null-fallback-zero", `{"Items":[{"PretaxAmount":null,"PaymentAmount":"0.00"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bss.ParseBill(success(tc.data))
			if err != nil || (got.Amount != nil) != tc.present || got.Amount != nil && *got.Amount != 0 {
				t.Fatalf("optional bill fabricated/lost zero: %+v, %v", got, err)
			}
		})
	}
}

func TestPackageMissingNullAndRealZero(t *testing.T) {
	for _, tc := range []struct {
		name, remaining string
		count           int
	}{
		{"missing", ``, 0}, {"null", `,"RemainingAmount":null`, 0},
		{"numeric-zero", `,"RemainingAmount":0`, 1}, {"string-zero", `,"RemainingAmount":"0.00"`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bss.ParsePackages(success(`{"Instances":[{"ProductCode":"bailian","InstanceId":"fixture","TotalAmount":10` + tc.remaining + `}]}`))
			if err != nil || len(got) != tc.count || len(got) == 1 && (got[0].RemainingAmount != 0 || got[0].RemainingFraction != 0) {
				t.Fatalf("missing resource amount became zero or depleted package lost: %+v, %v", got, err)
			}
		})
	}
}

func TestCollectionsRejectIncompletePaginationMetadata(t *testing.T) {
	for _, meta := range []string{
		`"TotalCount":2`, `"TotalCount":0`, `"TotalCount":-1`, `"TotalCount":1.5`,
		`"NextToken":"next"`, `"NextToken":true`, `"NextToken":{}`,
		`"PageNum":2`, `"PageNum":0`, `"PageNum":1.5`, `"PageNum":"bad"`,
		`"PageSize":0`, `"PageSize":1.5`, `"PageSize":1`,
	} {
		t.Run(meta, func(t *testing.T) {
			if _, err := bss.ParseBill(success(`{"Items":[{"PretaxAmount":1}],` + meta + `}`)); err == nil || !strings.Contains(err.Error(), "pagination") {
				t.Errorf("bill accepted unproven complete page: %v", err)
			}
			if _, err := bss.ParsePackages(success(`{"Instances":[{"ProductCode":"bailian","InstanceId":"fixture","RemainingAmount":1,"TotalAmount":2}],` + meta + `}`)); err == nil || !strings.Contains(err.Error(), "pagination") {
				t.Errorf("packages accepted unproven complete page: %v", err)
			}
		})
	}
}

func TestCollectionsAcceptExplicitCompleteSinglePage(t *testing.T) {
	got, err := bss.ParseBill(success(`{"Items":[{"PretaxAmount":0}],"TotalCount":1,"PageNum":1,"PageSize":1,"NextToken":null}`))
	if err != nil || got.Amount == nil || *got.Amount != 0 {
		t.Fatalf("complete bill rejected: %+v, %v", got, err)
	}
	packages, err := bss.ParsePackages(success(`{"Instances":[],"TotalCount":0,"PageNum":1,"PageSize":20,"NextToken":""}`))
	if err != nil || len(packages) != 0 {
		t.Fatalf("complete empty packages rejected: %+v, %v", packages, err)
	}
}
