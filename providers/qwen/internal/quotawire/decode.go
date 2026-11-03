package quotawire

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Result describes the legacy CLI JSON contract, not Coding Plan ownership.
// Pointers retain the distinction between missing/null and actual numeric zero.
type Result struct {
	Source     string `json:"source"`
	Plan       string `json:"plan"`
	PlanStatus string `json:"planStatus"`
	PlanStart  string `json:"planStart"`
	PlanEnd    string `json:"planEnd"`
	DaysLeft   *int   `json:"daysLeft"`
	ObservedAt string `json:"observedAt"`
	Windows    []struct {
		Window      string   `json:"window"`
		UsedPercent *float64 `json:"usedPercent"`
		ResetTime   string   `json:"resetTime"`
	} `json:"windows"`
	Metrics []struct {
		Key      string   `json:"key"`
		Label    string   `json:"label"`
		Value    *float64 `json:"value"`
		Unit     string   `json:"unit"`
		Currency string   `json:"currency"`
		Format   string   `json:"format"`
	} `json:"metrics"`
	Notes   []string `json:"notes"`
	Error   string   `json:"error"`
	Message string   `json:"message"`
}

func Decode(body []byte, displayName string) (pluginapi.QuotaFetchResponse, error) {
	var raw Result
	if json.Unmarshal(body, &raw) != nil {
		return pluginapi.QuotaFetchResponse{}, errors.New("quota command stdout is not valid contract JSON")
	}
	return Normalize(raw, displayName)
}

func Normalize(raw Result, displayName string) (pluginapi.QuotaFetchResponse, error) {
	empty := pluginapi.QuotaFetchResponse{}
	if raw.Error != "" || raw.Message != "" {
		return empty, errors.New("quota command returned an error")
	}
	for _, value := range []string{raw.ObservedAt, raw.PlanStart, raw.PlanEnd} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return empty, errors.New("quota observation or plan time is invalid")
			}
		}
	}
	result := pluginapi.QuotaFetchResponse{}
	if strings.TrimSpace(raw.Plan) != "" {
		result.Subscription = &pluginapi.QuotaSubscription{Plan: raw.Plan}
	}
	buckets := make([]pluginapi.QuotaBucket, 0, len(raw.Windows))
	for _, window := range raw.Windows {
		// A missing reading is omitted, but cannot hide malformed metadata
		// supplied alongside it by a command.
		if window.ResetTime != "" {
			if _, err := time.Parse(time.RFC3339Nano, window.ResetTime); err != nil {
				return empty, errors.New("quota reset time is invalid")
			}
		}
		if window.UsedPercent == nil {
			continue
		}
		percent := *window.UsedPercent
		if strings.TrimSpace(window.Window) == "" || math.IsNaN(percent) || math.IsInf(percent, 0) {
			return empty, errors.New("quota window is invalid")
		}
		if percent < 0 || percent > 100 {
			return empty, errors.New("quota command returned usedPercent outside 0..100")
		}
		buckets = append(buckets, pluginapi.QuotaBucket{Window: window.Window, RemainingFraction: 1 - percent/100, ResetTime: window.ResetTime})
	}
	if len(buckets) > 0 {
		result.Groups = []pluginapi.QuotaGroup{{DisplayName: displayName, Buckets: buckets}}
	}
	for _, metric := range raw.Metrics {
		if metric.Value == nil {
			continue
		}
		if strings.TrimSpace(metric.Key) == "" || math.IsNaN(*metric.Value) || math.IsInf(*metric.Value, 0) {
			return empty, errors.New("quota metric is invalid")
		}
		if metric.Format != "" && metric.Format != "number" && metric.Format != "currency" {
			return empty, errors.New("quota metric format is invalid")
		}
		result.Summary = append(result.Summary, pluginapi.QuotaMetric{Key: metric.Key, Label: metric.Label, Value: *metric.Value, Unit: metric.Unit, Format: metric.Format, Currency: metric.Currency})
	}
	if len(buckets) == 0 && len(result.Summary) == 0 {
		return empty, errors.New("quota command returned no windows or metrics")
	}
	return result, nil
}
