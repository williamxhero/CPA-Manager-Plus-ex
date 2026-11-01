package fullrefresh

import (
	"context"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"time"
)

type Purpose string

const (
	Manual   Purpose = "manual"
	Periodic Purpose = "periodic"
	Recovery Purpose = "recovery"
)

type Kind string

const (
	Official    Kind = "official_balance"
	Credential  Kind = "credential_quota"
	Unavailable Kind = "unavailable"
)

// Requests carry host-issued authority unchanged. No secrets or policy IDs.
type SourceRequest struct {
	Kind       Kind      `json:"kind"`
	Official   *ob.Query `json:"official,omitempty"`
	Credential *cq.Query `json:"credential,omitempty"`
}
type SourceStatus struct {
	WalletProof      *WalletMembership        `json:"walletProof,omitempty"`
	SharedWallet     *SharedWalletObservation `json:"sharedWallet,omitempty"`
	RetryNotBeforeMS int64                    `json:"retryNotBeforeMs"`
	QueryCapable     bool                     `json:"queryCapable"`
	ID               string                   `json:"id,omitempty"`
	Request          SourceRequest            `json:"request"`
	Targets          []rc.Target              `json:"targets,omitempty"`
	State            string                   `json:"state"`
	Reason           string                   `json:"reason,omitempty"`
	ManualDisabled   bool                     `json:"manualDisabled"`
	Queried          bool                     `json:"queried"`
	Attempt          *balance.Observation     `json:"attempt,omitempty"`
	LastSuccess      *balance.Observation     `json:"lastSuccess,omitempty"`
	Stale            bool                     `json:"stale"`
	NextDueMS        int64                    `json:"nextDueMs"`
}
type Inventory struct {
	LatestRound          *Progress      `json:"latestRound,omitempty"`
	SchedulerCheckedAtMS int64          `json:"schedulerCheckedAtMs"`
	NextDueMS            int64          `json:"nextDueMs"`
	Scope                rc.Scope       `json:"scope"`
	ResourceCount        int            `json:"resourceCount"`
	UnidentifiedCount    int            `json:"unidentifiedCount"`
	Sources              []SourceStatus `json:"sources"`
}
type Item struct {
	ExplicitRefresh      bool   `json:"explicitRefresh,omitempty"`
	AdmissionNotBeforeMS int64  `json:"admissionNotBeforeMs,omitempty"`
	SharedFromID         string `json:"sharedFromId,omitempty"`
	SourceStatus
	JobID   string `json:"jobId,omitempty"`
	Outcome string `json:"outcome"`
	Joined  bool   `json:"joined"`
}
type Progress struct {
	QueueDeadlineMS   int64    `json:"queueDeadlineMs"`
	PeriodicNextDueMS int64    `json:"periodicNextDueMs,omitempty"`
	NotDue            int      `json:"notDue"`
	Shared            int      `json:"shared"`
	ID                string   `json:"id"`
	Purpose           Purpose  `json:"purpose"`
	Scope             rc.Scope `json:"scope"`
	State             string   `json:"state"`
	StartedAtMS       int64    `json:"startedAtMs"`
	FinishedAtMS      int64    `json:"finishedAtMs,omitempty"`
	ResourceCount     int      `json:"resourceCount"`
	Succeeded         int      `json:"succeeded"`
	Failed            int      `json:"failed"`
	NotQueried        int      `json:"notQueried"`
	Pending           int      `json:"pending"`
	Items             []Item   `json:"items"`
}

// Purpose is context, NOT source authority or a routing gate ACK.
type CompletedObservation struct {
	WalletMembers []WalletMembership
	Purpose       Purpose
	Source        SourceRequest
	AttemptID     string
	Official      *ob.Status
	Credential    *cq.Status
}
type Observer func(context.Context, CompletedObservation)

// Clock controls scheduling only; NEVER observation freshness.
type Clock interface {
	Now() time.Time
	Ticks() <-chan time.Time
}

// Membership is earned by an actual self endpoint validating the returned
// account ID for THIS source credential/config. Config labels alone never prove it.
type WalletMembership struct {
	GroupID                string        `json:"groupId"`
	Source                 SourceRequest `json:"source"`
	AccountID              string        `json:"accountId"`
	Origin                 string        `json:"origin"`
	Protocol               string        `json:"protocol"`
	NativeUnit             string        `json:"nativeUnit"`
	ConfigVersion          int64         `json:"configVersion"`
	AuthenticatedAttemptID string        `json:"authenticatedAttemptId"`
	AuthenticatedAtMS      int64         `json:"authenticatedAtMs"`
}

// A shared wallet read is NOT a new authentication success for the member key.
// Keep its exact leader binding and its own member authentication receipt separate.
type SharedWalletObservation struct {
	Source      SourceRequest       `json:"source"`
	AttemptID   string              `json:"attemptId"`
	Observation balance.Observation `json:"observation"`
	Membership  WalletMembership    `json:"membership"`
}

// StartedObservation is trusted host context after atomic job+attempt persistence
// and before provider I/O. Joining a job never recaptures a newer generation.
type StartedObservation struct {
	Purpose       Purpose
	Source        SourceRequest
	AttemptID     string
	DeadlineMS    int64
	WalletMembers []WalletMembership
}
type StartedObserver func(context.Context, StartedObservation) error
