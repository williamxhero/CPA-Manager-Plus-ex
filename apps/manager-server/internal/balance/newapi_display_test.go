package balance

import (
	"context"
	"testing"
)

func TestPublishedNewAPIDisplayUSDDoesNotApplyExchangeRate(t *testing.T) {
	display, ok := parseNewAPIDisplay([]byte(`{"success":true,"data":{"quota_per_unit":500000,"display_in_currency":true,"quota_display_type":"USD","usd_exchange_rate":7}}`))
	if !ok {
		t.Fatal("status")
	}
	amount, ok := display.amount("2280724")
	if !ok || display.currency != "USD" || amount != "4.561448" {
		t.Fatalf("currency=%s amount=%s ok=%v", display.currency, amount, ok)
	}
}

func TestPublishedNewAPIDisplayCNYUsesExchangeRate(t *testing.T) {
	display, ok := parseNewAPIDisplay([]byte(`{"data":{"quota_per_unit":500000,"display_in_currency":true,"quota_display_type":"CNY","usd_exchange_rate":1}}`))
	if !ok {
		t.Fatal("status")
	}
	amount, ok := display.amount("172900")
	if !ok || display.currency != "CNY" || amount != "0.3458" {
		t.Fatalf("currency=%s amount=%s ok=%v", display.currency, amount, ok)
	}
	rated, ok := parseNewAPIDisplay([]byte(`{"data":{"quota_per_unit":500000,"display_in_currency":true,"quota_display_type":"CNY","usd_exchange_rate":7}}`))
	if !ok {
		t.Fatal("rated status")
	}
	amount, ok = rated.amount("500000")
	if !ok || amount != "7" {
		t.Fatalf("rated amount=%s ok=%v", amount, ok)
	}
}

func TestPublishedNewAPIDisplayRejectsUndisplayableStatus(t *testing.T) {
	for _, body := range []string{
		`{"data":{"quota_per_unit":500000,"display_in_currency":false,"quota_display_type":"USD"}}`,
		`{"data":{"quota_per_unit":500000,"display_in_currency":true,"quota_display_type":"TOKEN"}}`,
		`{"data":{"display_in_currency":true,"quota_display_type":"USD"}}`,
	} {
		if _, ok := parseNewAPIDisplay([]byte(body)); ok {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestApplyPublishedNewAPIDisplayKeepsExplicitMoney(t *testing.T) {
	newAPIDisplayMu.Lock()
	delete(newAPIDisplayCache, "https://api-top.com")
	newAPIDisplayMu.Unlock()
	previous := loadNewAPIStatus
	loadNewAPIStatus = func(context.Context, string) ([]byte, error) {
		t.Fatal("explicit money must not fetch status")
		return nil, nil
	}
	t.Cleanup(func() { loadNewAPIStatus = previous })
	obs := Observation{QueryStatus: "success", Currency: "CNY", AmountDecimal: "0.3458", NativeQuota: finiteQuota("172900")}
	ApplyPublishedNewAPIDisplay(context.Background(), "https://api-top.com", &obs)
	if obs.Currency != "CNY" || obs.AmountDecimal != "0.3458" {
		t.Fatalf("overwritten %+v", obs)
	}
	converted := Observation{QueryStatus: "success", NativeQuota: finiteQuota("2280724"), Conversion: &NewAPIConversionEvidence{}}
	ApplyPublishedNewAPIDisplay(context.Background(), "https://api-top.com", &converted)
	if converted.AmountDecimal != "" || converted.Currency != "" {
		t.Fatalf("conversion overwritten %+v", converted)
	}
	foreign := Observation{QueryStatus: "success", NativeQuota: finiteQuota("2280724")}
	ApplyPublishedNewAPIDisplay(context.Background(), "https://example.test", &foreign)
	if foreign.AmountDecimal != "" {
		t.Fatalf("foreign origin projected %+v", foreign)
	}
}

func TestApplyPublishedNewAPIDisplayProjectsStoredQuota(t *testing.T) {
	newAPIDisplayMu.Lock()
	delete(newAPIDisplayCache, "https://api-top.com")
	delete(newAPIDisplayCache, "https://code28.ccwu.cc")
	newAPIDisplayMu.Unlock()
	previous := loadNewAPIStatus
	loadNewAPIStatus = func(_ context.Context, origin string) ([]byte, error) {
		switch origin {
		case "https://api-top.com":
			return []byte(`{"data":{"quota_per_unit":500000,"display_in_currency":true,"quota_display_type":"USD","usd_exchange_rate":7}}`), nil
		case "https://code28.ccwu.cc":
			return []byte(`{"data":{"quota_per_unit":500000,"display_in_currency":true,"quota_display_type":"USD","usd_exchange_rate":1}}`), nil
		default:
			t.Fatalf("unexpected origin %s", origin)
			return nil, nil
		}
	}
	t.Cleanup(func() { loadNewAPIStatus = previous })
	top := Observation{QueryStatus: "success", NativeQuota: finiteQuota("2280724")}
	ApplyPublishedNewAPIDisplay(context.Background(), "https://api-top.com", &top)
	if top.Currency != "USD" || top.AmountDecimal != "4.561448" {
		t.Fatalf("api-top %+v", top)
	}
	code := Observation{QueryStatus: "success", NativeQuota: finiteQuota("-13156")}
	ApplyPublishedNewAPIDisplay(context.Background(), "https://code28.ccwu.cc", &code)
	if code.Currency != "USD" || code.AmountDecimal != "-0.026312" {
		t.Fatalf("code28 %+v", code)
	}
}

func finiteQuota(remaining string) *NewAPINativeQuota {
	return &NewAPINativeQuota{Unit: "newapi_quota", State: "finite", Remaining: &remaining}
}
