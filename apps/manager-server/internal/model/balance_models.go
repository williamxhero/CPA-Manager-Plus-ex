package model

import "strings"

// Balance-related resource types
const (
	BalanceResourceTypeCredential = "credential"
	BalanceResourceTypeProvider   = "provider"
)

// Balance semantic types
const (
	BalanceSemanticAccountWallet  = "account_wallet"
	BalanceSemanticKeyRemaining   = "key_remaining"
	BalanceSemanticCreditBalance  = "credit_balance"
)

// Balance query result status
const (
	BalanceResultSuccess            = "success"
	BalanceResultUnsupported        = "unsupported"
	BalanceResultNeedsConfiguration = "needs_configuration"
	BalanceResultAuthError          = "auth_error"
	BalanceResultRateLimited        = "rate_limited"
	BalanceResultNetworkError       = "network_error"
	BalanceResultParseError         = "parse_error"
)

// Auto-suspension reasons
const (
	AutoSuspensionReasonLowBalance     = "low_balance"
	AutoSuspensionReasonNoBalance      = "no_balance"
	AutoSuspensionReasonQueryFailed    = "query_failed"
	AutoSuspensionReasonManualOverride = "manual_override"
)

// Auto-suspension statuses
const (
	AutoSuspensionStatusActive    = "active"
	AutoSuspensionStatusRecovered = "recovered"
)

// BalanceSource defines where and how to query balance for a resource
type BalanceSource struct {
	ID                int64
	ResourceType      string // credential / provider
	ResourceID        string // stable opaque ID
	ResourceIDVersion int64  // identity version, changes when resource is modified
	CPAInstanceID     string // CPA instance scope
	Adapter           string // deepinfra / openrouter / newapi / billing_json / auto
	EndpointURL       string // HTTPS endpoint, empty for auto-detected
	CredentialRef     string // encrypted reference to query credential (may differ from resource credential)
	AccountScope      string // account ID, user ID, or empty for key-level
	Currency          string // USD / CNY / credits / tokens / etc
	UnitMultiplier    string // decimal string for conversion (e.g. "1000" for NewAPI quota → USD)
	ConfigVersion     int64  // increments on config change, invalidates old observations
	CreatedAtMS       int64
	UpdatedAtMS       int64
}

// BalanceObservation records one balance query result
type BalanceObservation struct {
	ID              int64
	SourceID        int64
	QueryID         string // UUID for this query attempt
	Semantic        string // account_wallet / key_remaining / credit_balance
	AmountDecimal   string // decimal string, empty if window-based
	Currency        string
	WindowJSON      string // JSON array of quota windows, empty if amount-based
	QueryStatus     string // success / unsupported / needs_configuration / etc
	ErrorCode       string // empty on success
	ErrorMessage    string // safe error, never includes credentials
	ObservedAtMS    int64  // when the balance was observed
	EvidenceHash    string // SHA256 of redacted evidence for dedup
	CreatedAtMS     int64
}

// BalanceRefreshState tracks when a resource needs its next balance refresh
type BalanceRefreshState struct {
	ID              int64
	SourceID        int64
	LastAttemptMS   int64
	LastSuccessMS   int64
	LastResultStatus string
	LastError       string
	NextRefreshMS   int64  // when to refresh next
	LeaseOwner      string // worker ID holding refresh lease
	LeaseExpiresMS  int64
	BatchID         string // groups concurrent refreshes
	CreatedAtMS     int64
	UpdatedAtMS     int64
}

// AutoSuspension records independent auto-stop state separate from manual disable
type AutoSuspension struct {
	ID                 int64
	ResourceType       string
	ResourceID         string
	ResourceIDVersion  int64
	Reason             string // low_balance / no_balance / etc
	Owner              string // cpamp_balance_monitor
	EvidenceObservationID int64 // which observation triggered suspension
	Status             string // active / recovered
	SuspendedAtMS      int64
	RecoveredAtMS      int64
	RecoveryEvidenceID int64 // which observation confirmed recovery
	CreatedAtMS        int64
	UpdatedAtMS        int64
}

// BalancePolicy defines auto-suspension policy for a resource
type BalancePolicy struct {
	ID                 int64
	ResourceType       string // credential / provider
	ResourceID         string // empty means global default
	AutoSuspendEnabled bool
	ThresholdDecimal   string // decimal string for threshold (e.g. "0.1")
	ThresholdCurrency  string
	RecoveryThresholdDecimal string // must exceed this to recover (e.g. "0.1")
	PolicyLocked       bool   // if true, can't be changed without admin
	LockedBy           string // who locked it
	CreatedAtMS        int64
	UpdatedAtMS        int64
}

// NormalizeBalanceResourceType normalizes resource type to standard values
func NormalizeBalanceResourceType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case BalanceResourceTypeCredential, "cred", "credential", "auth", "key":
		return BalanceResourceTypeCredential
	case BalanceResourceTypeProvider, "prov", "provider":
		return BalanceResourceTypeProvider
	default:
		return ""
	}
}

// NormalizeBalanceSemantic normalizes balance semantic to standard values
func NormalizeBalanceSemantic(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case BalanceSemanticAccountWallet, "wallet", "account", "account_balance":
		return BalanceSemanticAccountWallet
	case BalanceSemanticKeyRemaining, "key", "remaining", "key_limit":
		return BalanceSemanticKeyRemaining
	case BalanceSemanticCreditBalance, "credit", "credits", "balance":
		return BalanceSemanticCreditBalance
	default:
		return normalized
	}
}
