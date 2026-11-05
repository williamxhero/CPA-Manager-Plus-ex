package officialbalance

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const knownEmptyBalance = "—"

var (
	displayCurrency = regexp.MustCompile(`^[A-Z]{3}$`)
	displayAmount   = regexp.MustCompile(`^-?(?:0|[1-9]\d{0,59})(?:\.\d{1,30})?$`)
	balanceKeyID    = regexp.MustCompile(`^(?:gemini|interactions|codex|xai|meta|claude|vertex|openai):([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)
)

func observationDisplayText(obs balance.Observation) string {
	if obs.QueryStatus != "success" {
		return ""
	}
	if text := moneyText(obs.Currency, obs.AmountDecimal); text != "" {
		return text
	}
	if len(obs.Balances) > 0 {
		if text := moneyText(obs.Balances[0].Currency, obs.Balances[0].Total); text != "" {
			return text
		}
	}
	if obs.NativeQuota != nil && obs.NativeQuota.State == "unlimited" {
		return "unlimited"
	}
	return ""
}

func moneyText(currency, amount string) string {
	if !displayCurrency.MatchString(currency) || !displayAmount.MatchString(amount) {
		return ""
	}
	return currency + " " + amount
}

// RestoreCachedBalances fills a cached em dash from the one saved observation
// that still names that resource. A newer amount is left alone, and two
// different observations for the same resource stay unresolved.
func (s *Service) RestoreCachedBalances(ctx context.Context, texts map[string]string) {
	if s == nil || s.Store == nil || len(texts) == 0 {
		return
	}
	records, err := s.Store.ListOfficialBalances(ctx)
	if err != nil {
		return
	}
	saved := balanceTextByResource(records)
	for key, value := range texts {
		if value != knownEmptyBalance {
			continue
		}
		match := balanceKeyID.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		if text, ok := saved[strings.ToLower(match[1])]; ok {
			texts[key] = text
		}
	}
}

func balanceTextByResource(records []store.OfficialBalanceRecord) map[string]string {
	claims := map[string]string{}
	blocked := map[string]bool{}
	for _, record := range records {
		var obs balance.Observation
		if record.LastSuccess == "" || json.Unmarshal([]byte(record.LastSuccess), &obs) != nil {
			continue
		}
		text := observationDisplayText(obs)
		if text == "" {
			continue
		}
		var cfg Config
		if json.Unmarshal([]byte(record.Config), &cfg) != nil {
			continue
		}
		for _, target := range cfg.Targets {
			id := strings.ToLower(strings.TrimSpace(target.ResourceID))
			if id == "" || blocked[id] {
				continue
			}
			if previous, ok := claims[id]; ok && previous != text {
				delete(claims, id)
				blocked[id] = true
				continue
			}
			claims[id] = text
		}
	}
	return claims
}
