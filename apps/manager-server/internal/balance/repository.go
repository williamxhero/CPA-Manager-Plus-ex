package balance

import (
	"context"
)

// Repository manages balance-related data persistence
type Repository interface {
	// Balance sources
	CreateBalanceSource(ctx context.Context, source *BalanceSource) error
	GetBalanceSource(ctx context.Context, resourceType, resourceID string) (*BalanceSource, error)
	ListBalanceSources(ctx context.Context) ([]*BalanceSource, error)

	// Observations
	CreateObservation(ctx context.Context, obs *BalanceObservation) error
	GetLatestObservation(ctx context.Context, sourceID int64) (*BalanceObservation, error)

	// Refresh state
	UpsertRefreshState(ctx context.Context, state *BalanceRefreshState) error
	GetRefreshState(ctx context.Context, sourceID int64) (*BalanceRefreshState, error)
	ListStaleRefreshStates(ctx context.Context, beforeMS int64) ([]*BalanceRefreshState, error)

	// Auto-suspension
	UpsertAutoSuspension(ctx context.Context, suspension *AutoSuspension) error
	GetAutoSuspension(ctx context.Context, resourceType, resourceID string) (*AutoSuspension, error)
	ListActiveSuspensions(ctx context.Context) ([]*AutoSuspension, error)

	// Policy
	UpsertPolicy(ctx context.Context, policy *BalancePolicy) error
	GetPolicy(ctx context.Context, resourceType, resourceID string) (*BalancePolicy, error)
	GetDefaultPolicy(ctx context.Context, resourceType string) (*BalancePolicy, error)
}

// BalanceSource model
type BalanceSource struct {
	ID                int64
	ResourceType      string
	ResourceID        string
	ResourceIDVersion int64
	CPAInstanceID     string
	Adapter           string
	EndpointURL       string
	CredentialRef     string
	AccountScope      string
	Currency          string
	UnitMultiplier    string
	ConfigVersion     int64
	CreatedAtMS       int64
	UpdatedAtMS       int64
}

// BalanceObservation model
type BalanceObservation struct {
	ID            int64
	SourceID      int64
	QueryID       string
	Semantic      string
	AmountDecimal string
	Currency      string
	WindowJSON    string
	QueryStatus   string
	ErrorCode     string
	ErrorMessage  string
	ObservedAtMS  int64
	EvidenceHash  string
	CreatedAtMS   int64
}

// BalanceRefreshState model
type BalanceRefreshState struct {
	ID               int64
	SourceID         int64
	LastAttemptMS    int64
	LastSuccessMS    int64
	LastResultStatus string
	LastError        string
	NextRefreshMS    int64
	LeaseOwner       string
	LeaseExpiresMS   int64
	BatchID          string
	CreatedAtMS      int64
	UpdatedAtMS      int64
}

// AutoSuspension model
type AutoSuspension struct {
	ID                    int64
	ResourceType          string
	ResourceID            string
	ResourceIDVersion     int64
	Reason                string
	Owner                 string
	EvidenceObservationID int64
	Status                string
	SuspendedAtMS         int64
	RecoveredAtMS         int64
	RecoveryEvidenceID    int64
	CreatedAtMS           int64
	UpdatedAtMS           int64
}

// BalancePolicy model
type BalancePolicy struct {
	ID                       int64
	ResourceType             string
	ResourceID               string
	AutoSuspendEnabled       bool
	ThresholdDecimal         string
	ThresholdCurrency        string
	RecoveryThresholdDecimal string
	PolicyLocked             bool
	LockedBy                 string
	CreatedAtMS              int64
	UpdatedAtMS              int64
}
