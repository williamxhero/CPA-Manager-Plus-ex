// Package balance provides unified balance querying across AI provider billing systems.
//
// Architecture:
// - Small host interface: list capabilities, query one target, standardized observation
// - Deep module: protocol selection, auth, HTTP, parsing, normalization, safe errors
// - No direct config/routing modification: host owns resource enumeration, scheduling, persistence, state
//
// Security:
// - HTTPS only, certificate validation, response limits, timeout
// - Deny cloud metadata, unauthorized internal targets, DNS rebinding
// - Query credentials never in logs/errors/responses
// - Private self-hosted sites require explicit admin authorization
package balance

import (
	"context"
	"encoding/json"
	"time"
)

// QueryTarget identifies what to query
type QueryTarget struct {
	// Opaque resource ID (never raw API key)
	ResourceID     string
	PrivateTargets []string
	// Resource identity version
	IDVersion int64
	// Protocol/platform selection
	Adapter string
	// Endpoint URL (validated HTTPS, or empty for auto-detection)
	EndpointURL string
	// Credential reference (opaque, resolved by host)
	CredentialRef string
	// Account scope (user ID, account ID, or empty for key-level)
	AccountScope string
	// Currency/unit info for interpretation
	Currency       string
	UnitMultiplier string // decimal string
	// Cancel/timeout
	Timeout time.Duration
}

// Observation is the standardized query result
type CurrencyBalance struct {
	Currency string `json:"currency"`
	Total    string `json:"total"`
	Granted  string `json:"granted"`
	ToppedUp string `json:"toppedUp"`
}

type Observation struct {
	DeadlineMS int64             `json:"deadlineMs,omitempty"`
	Binding    json.RawMessage   `json:"binding,omitempty"`
	Balances   []CurrencyBalance `json:"balances,omitempty"`
	Available  *bool             `json:"available,omitempty"`
	// Query identity
	QueryID string `json:"queryId,omitempty"`
	// Capability status
	CapabilityStatus string `json:"capabilityStatus,omitempty"` // supported / unsupported / needs_configuration
	// Balance semantic
	Semantic string `json:"semantic,omitempty"` // account_wallet / key_remaining / credit_balance
	// Amount (decimal string, empty if window-based)
	AmountDecimal string `json:"amountDecimal,omitempty"`
	Currency      string `json:"currency,omitempty"`
	// Or quota windows (JSON, empty if amount-based)
	WindowsJSON string `json:"windowsJson,omitempty"`
	// Query result
	QueryStatus  string `json:"queryStatus,omitempty"` // success / auth_error / rate_limited / network_error / parse_error
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"` // safe, no credentials
	// Observation time
	ObservedAtMS int64 `json:"observedAtMs,omitempty"`
	// Evidence identity (for dedup, never stores raw response)
	EvidenceHash string `json:"evidenceHash,omitempty"`
}

// Adapter capability
type AdapterCapability struct {
	Adapter          string
	Supported        bool
	RequiresConfig   bool
	ConfigFields     []string // e.g. ["query_credential", "user_id", "unit_multiplier"]
	SupportedScopes  []string // e.g. ["account", "key"]
	SupportedFormats []string // e.g. ["amount", "window"]
}

// Querier is the deep module interface
type Querier interface {
	// ListCapabilities returns supported adapters and their requirements
	ListCapabilities(ctx context.Context) ([]AdapterCapability, error)

	// Query executes one balance query and returns standardized observation
	Query(ctx context.Context, target QueryTarget, credentialResolver CredentialResolver) (Observation, error)
}

// CredentialResolver is provided by host to resolve credential references
// without exposing raw credentials to the balance module
type CredentialResolver interface {
	// ResolveCredential returns the actual credential value for a reference
	// The balance module never logs or persists this value
	ResolveCredential(ctx context.Context, ref string) (string, error)
}

// Protocol adapter interface (internal to balance module)
type protocolAdapter interface {
	// Name returns adapter identifier
	Name() string
	// Capability returns what this adapter supports
	Capability() AdapterCapability
	// Query executes protocol-specific query
	Query(ctx context.Context, target QueryTarget, credential string) (Observation, error)
}
