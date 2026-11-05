package balance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Published NewAPI display is the instance's own /api/status contract.
// USD is remaining/quota_per_unit. CNY multiplies that result by the same
// document's usd_exchange_rate. A UI rate is never applied on top of USD.
const newAPIStatusPath = "/api/status"

type newAPIStatusFields struct {
	Data              json.RawMessage `json:"data"`
	QuotaPerUnit      json.RawMessage `json:"quota_per_unit"`
	DisplayInCurrency *bool           `json:"display_in_currency"`
	QuotaDisplayType  string          `json:"quota_display_type"`
	USDExchangeRate   json.RawMessage `json:"usd_exchange_rate"`
}

type publishedNewAPIDisplay struct {
	currency  string
	perUnit   *big.Int
	rateNumer *big.Int
	rateScale int
	expires   time.Time
}

var (
	newAPIDisplayMu    sync.Mutex
	newAPIDisplayCache = map[string]publishedNewAPIDisplay{}
	loadNewAPIStatus   = fetchNewAPIStatus
	newAPIStatusClient = &http.Client{
		Timeout: 4 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 2 {
				return errors.New("too many redirects")
			}
			if !enrolledBrowserOrigin(req.URL.Scheme + "://" + req.URL.Host) {
				return errors.New("redirect left enrolled origin")
			}
			return nil
		},
	}
)

func enrolledBrowserOrigin(origin string) bool {
	_, ok := browserSessionOrigins[strings.TrimRight(origin, "/")]
	return ok
}

// ApplyPublishedNewAPIDisplay fills currency and amount from the origin's
// public display settings. An observation that already has money or an
// explicit conversion is left unchanged. Origins outside the browser-session
// allowlist are ignored.
func ApplyPublishedNewAPIDisplay(ctx context.Context, origin string, obs *Observation) {
	if obs == nil || obs.QueryStatus != "success" || obs.Conversion != nil || obs.AmountDecimal != "" || obs.Currency != "" {
		return
	}
	if obs.NativeQuota == nil || obs.NativeQuota.State != "finite" || obs.NativeQuota.Remaining == nil {
		return
	}
	origin = strings.TrimRight(origin, "/")
	if !enrolledBrowserOrigin(origin) {
		return
	}
	display, ok := publishedDisplay(ctx, origin)
	if !ok {
		return
	}
	amount, ok := display.amount(*obs.NativeQuota.Remaining)
	if !ok {
		return
	}
	obs.Currency = display.currency
	obs.AmountDecimal = amount
}

func (d publishedNewAPIDisplay) amount(remaining string) (string, bool) {
	numer, ok := new(big.Int).SetString(remaining, 10)
	if !ok || d.perUnit == nil || d.perUnit.Sign() <= 0 || d.rateNumer == nil || d.rateNumer.Sign() <= 0 {
		return "", false
	}
	numer.Mul(numer, d.rateNumer)
	denom := new(big.Int).Mul(new(big.Int).Set(d.perUnit), pow10(d.rateScale))
	return exactQuotient(numer, denom)
}

func publishedDisplay(ctx context.Context, origin string) (publishedNewAPIDisplay, bool) {
	now := time.Now()
	newAPIDisplayMu.Lock()
	cached, ok := newAPIDisplayCache[origin]
	newAPIDisplayMu.Unlock()
	if ok && now.Before(cached.expires) && cached.perUnit != nil {
		return cached, true
	}
	body, err := loadNewAPIStatus(ctx, origin)
	if err != nil {
		rememberDisplay(origin, publishedNewAPIDisplay{expires: now.Add(30 * time.Second)})
		return publishedNewAPIDisplay{}, false
	}
	display, ok := parseNewAPIDisplay(body)
	if !ok {
		rememberDisplay(origin, publishedNewAPIDisplay{expires: now.Add(30 * time.Second)})
		return publishedNewAPIDisplay{}, false
	}
	display.expires = now.Add(10 * time.Minute)
	rememberDisplay(origin, display)
	return display, true
}

func rememberDisplay(origin string, display publishedNewAPIDisplay) {
	newAPIDisplayMu.Lock()
	newAPIDisplayCache[origin] = display
	newAPIDisplayMu.Unlock()
}

func fetchNewAPIStatus(ctx context.Context, origin string) ([]byte, error) {
	if !enrolledBrowserOrigin(origin) {
		return nil, errors.New("origin not enrolled")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(origin, "/")+newAPIStatusPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := newAPIStatusClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("status unavailable")
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<18))
}

func parseNewAPIDisplay(body []byte) (publishedNewAPIDisplay, bool) {
	var doc newAPIStatusFields
	if json.Unmarshal(body, &doc) != nil {
		return publishedNewAPIDisplay{}, false
	}
	if len(doc.Data) > 0 && doc.Data[0] == '{' {
		if json.Unmarshal(doc.Data, &doc) != nil {
			return publishedNewAPIDisplay{}, false
		}
	}
	if doc.DisplayInCurrency == nil || !*doc.DisplayInCurrency {
		return publishedNewAPIDisplay{}, false
	}
	perUnit, scale, ok := numericDecimal(doc.QuotaPerUnit)
	if !ok || scale != 0 || perUnit.Sign() <= 0 {
		return publishedNewAPIDisplay{}, false
	}
	kind := strings.TrimSpace(doc.QuotaDisplayType)
	switch kind {
	case "", "USD":
		return publishedNewAPIDisplay{currency: "USD", perUnit: perUnit, rateNumer: big.NewInt(1), rateScale: 0}, true
	case "CNY":
		rate, rateScale, ok := numericDecimal(doc.USDExchangeRate)
		if !ok || rate.Sign() <= 0 {
			return publishedNewAPIDisplay{}, false
		}
		return publishedNewAPIDisplay{currency: "CNY", perUnit: perUnit, rateNumer: rate, rateScale: rateScale}, true
	default:
		return publishedNewAPIDisplay{}, false
	}
}

func exactQuotient(numer, denom *big.Int) (string, bool) {
	if denom.Sign() <= 0 {
		return "", false
	}
	for scale := 0; scale <= 8; scale++ {
		scaled := new(big.Int).Mul(numer, pow10(scale))
		if new(big.Int).Rem(scaled, denom).Sign() == 0 {
			return formatExactDecimal(new(big.Int).Quo(scaled, denom), scale), true
		}
	}
	return "", false
}

func pow10(scale int) *big.Int {
	if scale < 0 {
		scale = 0
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
}
