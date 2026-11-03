package bss

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type BalanceResult struct {
	AvailableAmount     *float64
	AvailableCashAmount *float64
	CreditAmount        *float64
	Currency            string
}

type BillResult struct {
	Amount   *float64
	Currency string
}

type ResourcePackage struct {
	ID                string
	Name              string
	Product           string
	Unit              string
	RemainingAmount   float64
	TotalAmount       float64
	RemainingFraction float64
	ExpiresAt         *time.Time
}

type object map[string]json.RawMessage

func decodeEnvelope(body []byte) (object, error) {
	if len(body) > MaxResponseBytes {
		return nil, errors.New("bss: response exceeds size limit")
	}
	var envelope object
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return nil, errors.New("bss: invalid JSON response")
	}
	code := text(envelope["Code"])
	var success bool
	_ = json.Unmarshal(envelope["Success"], &success)
	if !success || code != "200" {
		if !safeCode.MatchString(code) {
			code = "unknown"
		}
		message := safeText(text(envelope["Message"]))
		return nil, fmt.Errorf("bss: API error %s: %s", code, message)
	}
	var data object
	if err := json.Unmarshal(envelope["Data"], &data); err != nil || data == nil {
		return nil, errors.New("bss: response has no valid Data object")
	}
	return data, nil
}

func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

var groupedAmount = regexp.MustCompile(`^[+-]?[0-9]{1,3}(,[0-9]{3})+(\.[0-9]+)?$`)

func number(raw json.RawMessage) (*float64, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var literal any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&literal) != nil {
		return nil, errors.New("invalid amount")
	}
	switch literal.(type) {
	case string, json.Number:
	default:
		return nil, errors.New("invalid amount")
	}
	s := strings.TrimSpace(text(raw))
	if s == "" {
		return nil, nil
	}
	// BSS formats balances as strings and may include thousands separators.
	if strings.Contains(s, ",") {
		if !groupedAmount.MatchString(s) {
			return nil, errors.New("invalid amount")
		}
		s = strings.ReplaceAll(s, ",", "")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, errors.New("invalid amount")
	}
	return &v, nil
}

func ParseBalance(body []byte) (BalanceResult, error) {
	data, err := decodeEnvelope(body)
	if err != nil {
		return BalanceResult{}, err
	}
	result := BalanceResult{Currency: text(data["Currency"])}
	for _, field := range []struct {
		name string
		dest **float64
	}{
		{"AvailableAmount", &result.AvailableAmount}, {"AvailableCashAmount", &result.AvailableCashAmount}, {"CreditAmount", &result.CreditAmount},
	} {
		value, err := number(data[field.name])
		if err != nil {
			return BalanceResult{}, fmt.Errorf("bss: invalid %s", field.name)
		}
		*field.dest = value
	}
	if result.AvailableAmount == nil && result.AvailableCashAmount == nil && result.CreditAmount == nil {
		return BalanceResult{}, errors.New("bss: balance response contains no amounts")
	}
	return result, nil
}

// itemList accepts the BSS RPC wrappers and direct arrays used by API versions.
func itemList(data object, container, item string) ([]object, error) {
	raw, ok := data[container]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = json.RawMessage("[]")
	}
	var wrapper object
	if json.Unmarshal(raw, &wrapper) == nil && wrapper != nil {
		raw, ok = wrapper[item]
		if !ok {
			return nil, errors.New("bss: malformed item collection")
		}
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = json.RawMessage("[]")
	}
	var items []object
	if json.Unmarshal(raw, &items) != nil {
		return nil, errors.New("bss: malformed item collection")
	}
	for _, entry := range items {
		if entry == nil {
			return nil, errors.New("bss: malformed item collection")
		}
	}
	if !completePage(data, len(items)) {
		return nil, errors.New("bss: response is incomplete; pagination required")
	}
	return items, nil
}

func completePage(data object, itemCount int) bool {
	count, err := number(data["TotalCount"])
	if err != nil || count != nil && *count != float64(itemCount) {
		return false
	}
	page, err := number(data["PageNum"])
	if err != nil || page != nil && *page != 1 {
		return false
	}
	size, err := number(data["PageSize"])
	if err != nil || size != nil && (*size <= 0 || *size != math.Trunc(*size) || *size < float64(itemCount) || count == nil && *size == float64(itemCount)) {
		return false
	}
	var next string
	if raw := data["NextToken"]; len(raw) != 0 && json.Unmarshal(raw, &next) != nil {
		return false
	}
	return next == ""
}

func ParseBill(body []byte) (BillResult, error) {
	data, err := decodeEnvelope(body)
	if err != nil {
		return BillResult{}, err
	}
	items, err := itemList(data, "Items", "Item")
	if err != nil {
		return BillResult{}, err
	}
	result := BillResult{Currency: text(data["Currency"])}
	var sum float64
	present := false
	for _, item := range items {
		amount, err := number(item["PretaxAmount"])
		if err == nil && amount == nil {
			amount, err = number(item["PaymentAmount"])
		}
		if err != nil {
			return BillResult{}, errors.New("bss: invalid bill amount")
		}
		if amount == nil {
			return BillResult{}, errors.New("bss: bill item contains no amount")
		}
		currency := text(item["Currency"])
		if currency != "" {
			if result.Currency != "" && currency != result.Currency {
				return BillResult{}, errors.New("bss: bill contains mixed currencies")
			}
			result.Currency = currency
		}
		sum += *amount
		if math.IsInf(sum, 0) || math.IsNaN(sum) {
			return BillResult{}, errors.New("bss: bill total overflow")
		}
		present = true
	}
	if present {
		result.Amount = &sum
	}
	return result, nil
}

func ParsePackages(body []byte) ([]ResourcePackage, error) {
	data, err := decodeEnvelope(body)
	if err != nil {
		return nil, err
	}
	container, itemName := "Instances", "Instance"
	if _, ok := data[container]; !ok {
		container, itemName = "Items", "Item"
	}
	items, err := itemList(data, container, itemName)
	if err != nil {
		return nil, err
	}
	var result []ResourcePackage
	for _, item := range items {
		var products []string
		for _, key := range []string{"ProductCode", "ProductName", "CommodityCode", "ApplicableProducts"} {
			collectStrings(item[key], &products)
		}
		product := strings.Join(products, " ")
		lower := strings.ToLower(product)
		if !strings.Contains(lower, "bailian") && !strings.Contains(lower, "model studio") && !strings.Contains(lower, "modelstudio") && !strings.Contains(product, "百炼") {
			continue
		}
		remaining, err := number(item["RemainingAmount"])
		if err != nil || remaining == nil || *remaining < 0 {
			continue
		}
		total, err := number(item["TotalAmount"])
		if err != nil || total == nil || *total <= 0 || *remaining > *total {
			continue
		}
		name := text(item["PackageName"])
		if name == "" {
			name = text(item["InstanceName"])
		}
		id := text(item["InstanceId"])
		if name == "" {
			name = id
		}
		if name == "" {
			continue
		}
		expiry := text(item["ExpiryTime"])
		if expiry == "" {
			expiry = text(item["ExpirationTime"])
		}
		result = append(result, ResourcePackage{
			ID: id, Name: name, Product: product, Unit: text(item["Unit"]),
			RemainingAmount: *remaining, TotalAmount: *total, RemainingFraction: *remaining / *total,
			ExpiresAt: parseExpiry(expiry),
		})
	}
	return result, nil
}

func collectStrings(raw json.RawMessage, stringsOut *[]string) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			*stringsOut = append(*stringsOut, v)
		case []any:
			for _, child := range v {
				walk(child)
			}
		case map[string]any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(value)
}

func parseExpiry(value string) *time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02 15:04:05", "2006-01-02"} {
		if expiry, err := time.Parse(layout, value); err == nil {
			return &expiry
		}
	}
	return nil
}
