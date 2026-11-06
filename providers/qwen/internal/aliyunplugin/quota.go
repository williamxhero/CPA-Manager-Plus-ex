package aliyunplugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	config "cpa-credential-qwen/internal/aliyunconfig"
)

func (m *Manager) FetchQuota(req pluginapi.QuotaFetchRequest) (*pluginapi.QuotaFetchResponse, error) {
	m.lifeMu.RLock()
	defer m.lifeMu.RUnlock()
	m.mu.RLock()
	cfg, client, stopped := m.cfg, m.client, m.stopped
	m.mu.RUnlock()
	if cfg.DebugLogRequests && m.host != nil {
		// Request fields and even attribute names are untrusted and may be secrets.
		m.host.Log("info", fmt.Sprintf("quota.fetch attribute_count=%d has_secret=%t", len(req.Attributes), cfg.AccessKeySecret != ""))
	}
	if stopped || cfg.QuotaSource == "" || cfg.QuotaSource == config.SourceBSS && client == nil {
		return nil, errors.New("quota provider is not configured or has been shut down")
	}
	if !cfg.DebugLogRequests {
		matched := false
		for _, provider := range cfg.SupportedProviders {
			if provider == req.Provider {
				matched = true
				break
			}
		}
		if !matched {
			return nil, errors.New("quota provider does not support this credential provider")
		}
	}
	if cfg.QuotaSource == config.SourceCommand {
		return fetchCommand(cfg, req)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*cfg.RequestTimeout)
	defer cancel()
	balance, err := client.Balance(ctx)
	if err != nil {
		return nil, err
	}
	result := &pluginapi.QuotaFetchResponse{Groups: []pluginapi.QuotaGroup{{DisplayName: "阿里云账户"}}}
	currency := balance.Currency
	if currency == "" {
		currency = "CNY"
	}
	addMetric := func(key, label string, value *float64, currency string) {
		if value != nil {
			result.Summary = append(result.Summary, pluginapi.QuotaMetric{Key: key, Label: label, Value: *value, Unit: currency, Currency: currency, Format: "currency"})
		}
	}
	addMetric("balance_available", "阿里云可用余额", balance.AvailableAmount, currency)
	addMetric("balance_cash", "现金余额", balance.AvailableCashAmount, currency)
	addMetric("credit_limit", "信用额度", balance.CreditAmount, currency)
	if cfg.IncludeBill {
		// BSS billing cycles follow the China account calendar, not the host's local timezone.
		cycle := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01")
		bill, err := client.Bill(ctx, cycle)
		if err != nil {
			if m.host != nil {
				m.host.Log("warn", "optional monthly bill unavailable")
			}
		} else {
			billCurrency := bill.Currency
			if billCurrency == "" {
				billCurrency = currency
			}
			addMetric("month_spend", "本月消费", bill.Amount, billCurrency)
		}
	}
	if cfg.IncludeResourcePackages {
		packages, err := client.Packages(ctx)
		if err != nil {
			if m.host != nil {
				m.host.Log("warn", "optional resource packages unavailable")
			}
		} else {
			for _, pkg := range packages {
				name := pkg.Name
				if name == "" {
					name = pkg.ID
				}
				bucket := pluginapi.QuotaBucket{Window: "resource_package:" + name, RemainingFraction: pkg.RemainingFraction, Description: pkg.Product}
				if pkg.ExpiresAt != nil {
					bucket.ResetTime = pkg.ExpiresAt.UTC().Format(time.RFC3339)
				}
				result.Groups[0].Buckets = append(result.Groups[0].Buckets, bucket)
			}
		}
	}
	return result, nil
}
