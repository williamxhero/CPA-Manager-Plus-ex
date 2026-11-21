package balance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const DeepInfraBillingEndpoint = "https://api.deepinfra.com/payment/checklist"

func queryDeepInfraAccount(ctx context.Context, client *http.Client, bearer string) (map[string]json.RawMessage, string, bool) {
	// Account scope is optional. Do not spend the display query's deadline on it.
	probe, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(probe, "GET", "https://api.deepinfra.com/v1/me?checklist=true", nil)
	if err != nil {
		return nil, "", false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	res, err := client.Do(req)
	if err != nil {
		return nil, "", false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", false
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBodySize+1))
	if err != nil || len(raw) > maxResponseBodySize || !utf8.Valid(raw) {
		return nil, "", false
	}
	account, ok := checklistNumbers(raw)
	checklist, valid := checklistNumbers(account["checklist"])
	if !ok || !valid {
		return nil, "", false
	}
	var root string
	if json.Unmarshal(account["root_uid"], &root) != nil || root == "" || len(root) > 256 || root != strings.TrimSpace(root) {
		return nil, "", false
	}
	return checklist, root, true
}

func deepInfraProof(data map[string]json.RawMessage, payer string) *DeepInfraFunding {
	d := DeepInfraFunding{Payer: payer}
	if payer == "" || json.Unmarshal(data["last_checked"], &d.CheckedAt) != nil ||
		json.Unmarshal(data["suspended"], &d.Suspended) != nil || d.Suspended == nil {
		return nil
	}
	if bytes.Equal(data["suspend_reason"], []byte("null")) {
		d.Reason = "none"
	} else if json.Unmarshal(data["suspend_reason"], &d.Reason) != nil || d.Reason != "balance" {
		return nil
	}
	d.Stripe, d.Recent, d.Overdue = fundingNumber(data["stripe_balance"]), fundingNumber(data["recent"]), fundingNumber(data["overdue_invoices"])
	var buckets []json.RawMessage
	if d.Stripe == "" || d.Recent == "" || d.Overdue == "" {
		return nil
	}
	if *d.Suspended {
		if json.Unmarshal(data["scoped_credits"], &buckets) != nil || buckets == nil || len(buckets) > 32 {
			return nil
		}
		d.Credits = make([]DeepInfraCredit, 0, len(buckets))
		for _, raw := range buckets {
			fields, ok := checklistNumbers(raw)
			var credit DeepInfraCredit
			credit.Remaining = fundingNumber(fields["remaining_cents"])
			if !ok || credit.Remaining == "" ||
				json.Unmarshal(fields["expired"], &credit.Expired) != nil || credit.Expired == nil ||
				json.Unmarshal(fields["expires_ts"], &credit.ExpiresAt) != nil {
				return nil
			}
			d.Credits = append(d.Credits, credit)
		}
	}
	return &d
}

func fundingNumber(raw json.RawMessage) string {
	coefficient, scale, ok := numericDecimal(raw)
	if !ok {
		return ""
	}
	return formatExactDecimal(coefficient, scale)
}
