package model

import "strings"

const (
	QuotaCooldownOwnerUsage429     = "cpamp_usage_429"
	QuotaCooldownOwnerXAIFreeUsage = "cpamp_xai_free_usage"

	QuotaCooldownStatusActive    = "active"
	QuotaCooldownStatusRecovered = "recovered"
	QuotaCooldownStatusSkipped   = "skipped"

	// QuotaCooldownRecoverKindProvider marks a cooldown whose recovery time came
	// from the provider's own reset timestamp, so RecoverAtMS is a trustworthy
	// schedule and may be surfaced to the panel.
	QuotaCooldownRecoverKindProvider = "provider"
	// QuotaCooldownRecoverKindUnknown marks a cooldown the provider exhausted
	// without publishing any reset timestamp. RecoverAtMS is not a real recovery
	// time: recovery is driven only by conservative periodic quota checks at
	// NextCheckAtMS, and the read API must return recoverAtMs=0 so no fabricated
	// ETA reaches the panel.
	QuotaCooldownRecoverKindUnknown = "unknown"
)

// NormalizeQuotaCooldownRecoverKind defaults an empty kind to provider when a
// recovery timestamp is present, otherwise to unknown.
func NormalizeQuotaCooldownRecoverKind(kind string, recoverAtMS int64) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case QuotaCooldownRecoverKindProvider:
		return QuotaCooldownRecoverKindProvider
	case QuotaCooldownRecoverKindUnknown:
		return QuotaCooldownRecoverKindUnknown
	}
	if recoverAtMS > 0 {
		return QuotaCooldownRecoverKindProvider
	}
	return QuotaCooldownRecoverKindUnknown
}

type QuotaCooldown struct {
	ID               int64
	AuthFileName     string
	AuthIndex        string
	AccountSnapshot  string
	Provider         string
	ReasonCode       string
	WindowKind       string
	EvidenceJSON     string
	RecoverAtMS      int64
	RecoverAtKind    string
	NextCheckAtMS    int64
	Owner            string
	EventHash        string
	PreDisabledState bool
	Status           string
	DisabledAtMS     int64
	RecoveredAtMS    int64
	LastError        string
	CreatedAtMS      int64
	UpdatedAtMS      int64
}

type QuotaCooldownUpsert struct {
	AuthFileName        string
	AuthIndex           string
	AccountSnapshot     string
	Provider            string
	ReasonCode          string
	WindowKind          string
	EvidenceJSON        string
	RecoverAtMS         int64
	RecoverAtKind       string
	NextCheckAtMS       int64
	Owner               string
	EventHash           string
	PreDisabledState    bool
	ObservedEnabledAtMS int64
	DisabledAtMS        int64
}
