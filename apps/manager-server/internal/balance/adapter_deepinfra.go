package balance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	deepInfraDefaultEndpoint = "https://api.deepinfra.com/v1/billing"
)

type deepInfraAdapter struct {
	client *http.Client
}

func newDeepInfraAdapter() *deepInfraAdapter {
	return &deepInfraAdapter{
		client: newSecureHTTPClient(),
	}
}

func (a *deepInfraAdapter) Name() string {
	return "deepinfra"
}

func (a *deepInfraAdapter) Capability() AdapterCapability {
	return AdapterCapability{
		Adapter:          "deepinfra",
		Supported:        true,
		RequiresConfig:   false, // API key is sufficient
		ConfigFields:     []string{},
		SupportedScopes:  []string{"account"},
		SupportedFormats: []string{"amount"},
	}
}

func (a *deepInfraAdapter) Query(ctx context.Context, target QueryTarget, credential string) (Observation, error) {
	queryID := generateQueryID()
	now := time.Now().UnixMilli()

	// Build endpoint
	endpoint := target.EndpointURL
	if endpoint == "" {
		endpoint = deepInfraDefaultEndpoint
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGET, endpoint, nil)
	if err != nil {
		return Observation{
			QueryID:          queryID,
			CapabilityStatus: "supported",
			QueryStatus:      "network_error",
			ErrorCode:        "REQUEST_BUILD_FAILED",
			ErrorMessage:     safeError(err),
			ObservedAtMS:     now,
		}, nil
	}

	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Accept", "application/json")

	// Execute request
	resp, err := a.client.Do(req)
	if err != nil {
		return Observation{
			QueryID:          queryID,
			CapabilityStatus: "supported",
			QueryStatus:      "network_error",
			ErrorCode:        "HTTP_REQUEST_FAILED",
			ErrorMessage:     safeError(err),
			ObservedAtMS:     now,
		}, nil
	}
	defer resp.Body.Close()

	// Check status
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return Observation{
			QueryID:          queryID,
			CapabilityStatus: "supported",
			QueryStatus:      "auth_error",
			ErrorCode:        fmt.Sprintf("HTTP_%d", resp.StatusCode),
			ErrorMessage:     "authentication failed",
			ObservedAtMS:     now,
		}, nil
	}
	if resp.StatusCode == 429 {
		return Observation{
			QueryID:          queryID,
			CapabilityStatus: "supported",
			QueryStatus:      "rate_limited",
			ErrorCode:        "HTTP_429",
			ErrorMessage:     "rate limited",
			ObservedAtMS:     now,
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Observation{
			QueryID:          queryID,
			CapabilityStatus: "supported",
			QueryStatus:      "network_error",
			ErrorCode:        fmt.Sprintf("HTTP_%d", resp.StatusCode),
			ErrorMessage:     fmt.Sprintf("unexpected status %d", resp.StatusCode),
			ObservedAtMS:     now,
		}, nil
	}

	// Read response
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
	if err != nil {
		return Observation{
			QueryID:          queryID,
			CapabilityStatus: "supported",
			QueryStatus:      "network_error",
			ErrorCode:        "RESPONSE_READ_FAILED",
			ErrorMessage:     safeError(err),
			ObservedAtMS:     now,
		}, nil
	}

	// Parse response
	var billing deepInfraBillingResponse
	if err := json.Unmarshal(body, &billing); err != nil {
		return Observation{
			QueryID:          queryID,
			CapabilityStatus: "supported",
			QueryStatus:      "parse_error",
			ErrorCode:        "JSON_PARSE_FAILED",
			ErrorMessage:     "failed to parse billing response",
			ObservedAtMS:     now,
		}, nil
	}

	// Calculate available balance
	// DeepInfra formula (from conter_proj): available = -(stripe_balance + recent_usage)
	// Negative available means debt, positive means credit
	stripeBalance := billing.StripeBalance
	recentUsage := billing.RecentUsage
	if recentUsage == nil {
		zero := 0.0
		recentUsage = &zero
	}

	availableBalance := -(stripeBalance + *recentUsage)

	return Observation{
		QueryID:          queryID,
		CapabilityStatus: "supported",
		Semantic:         BalanceSemanticAccountWallet,
		AmountDecimal:    formatDecimal(availableBalance),
		Currency:         "USD",
		QueryStatus:      "success",
		ErrorCode:        "",
		ErrorMessage:     "",
		ObservedAtMS:     now,
		EvidenceHash:     hashEvidence(body),
	}, nil
}

type deepInfraBillingResponse struct {
	StripeBalance float64  `json:"stripe_balance"`
	RecentUsage   *float64 `json:"recent_usage"`
}

func formatDecimal(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func hashEvidence(body []byte) string {
	// TODO: Implement SHA256 hash of redacted evidence
	return fmt.Sprintf("ev-%d", len(body))
}
