package balance

import (
	"encoding/json"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Resource limits, not a claim about the vendor's maximum monetary precision:
// <=120 mantissa digits, exponent [-120,120] (<=3 digits), expanded integer
// and fractional parts <=120 digits each. JSON numbers only, never strings.
var jsonMoney = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]{1,3}))?$`)

func numericDecimal(raw json.RawMessage) (*big.Int, int, bool) {
	if len(raw) == 0 || len(raw) > 256 {
		return nil, 0, false
	}
	m := jsonMoney.FindStringSubmatch(string(raw))
	if m == nil || len(m[2])+len(m[3]) > 120 {
		return nil, 0, false
	}
	exponent := 0
	if m[4] != "" {
		exponent, _ = strconv.Atoi(m[4])
	}
	if exponent < -120 || exponent > 120 {
		return nil, 0, false
	}
	scale := len(m[3]) - exponent
	digits := m[2] + m[3]
	if scale > 120 || len(digits)-scale > 120 {
		return nil, 0, false
	}
	if scale < 0 {
		digits += strings.Repeat("0", -scale)
		scale = 0
	}
	n, ok := new(big.Int).SetString(m[1]+digits, 10)
	return n, scale, ok
}

func signedPrepaid(stripe, recent json.RawMessage) (string, bool) {
	a, as, ok := numericDecimal(stripe)
	if !ok {
		return "", false
	}
	b, bs, ok := numericDecimal(recent)
	if !ok {
		return "", false
	}
	scale := max(as, bs)
	power := func(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }
	a.Mul(a, power(scale-as))
	b.Mul(b, power(scale-bs))
	a.Add(a, b)
	a.Neg(a)
	if a.Sign() == 0 {
		return "0", true
	}
	negative := a.Sign() < 0
	digits := new(big.Int).Abs(a).String()
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	if scale > 0 {
		i := len(digits) - scale
		digits = digits[:i] + "." + digits[i:]
		digits = strings.TrimRight(strings.TrimRight(digits, "0"), ".")
	}
	if negative {
		digits = "-" + digits
	}
	return digits, true
}
