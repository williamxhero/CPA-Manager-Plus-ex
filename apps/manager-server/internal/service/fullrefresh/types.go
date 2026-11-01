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
	RetryNotBeforeMS int64                `json:"retryNotBeforeMs"`
	QueryCapable     bool                 `json:"queryCapable"`
	ID               string               `json:"id,omitempty"`
	Request          SourceRequest        `json:"request"`
	Targets          []rc.Target          `json:"targets,omitempty"`
	State            string               `json:"state"`
	Reason           string               `json:"reason,omitempty"`
	ManualDisabled   bool                 `json:"manualDisabled"`
	Queried          bool                 `json:"queried"`
	Attempt          *balance.Observation `json:"attempt,omitempty"`
	LastSuccess      *balance.Observation `json:"lastSuccess,omitempty"`
	Stale            bool                 `json:"stale"`
	NextDueMS        int64                `json:"nextDueMs"`
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
	SourceStatus
	JobID   string `json:"jobId,omitempty"`
	Outcome string `json:"outcome"`
	Joined  bool   `json:"joined"`
}
type Progress struct {
	ID            string   `json:"id"`
	Purpose       Purpose  `json:"purpose"`
	Scope         rc.Scope `json:"scope"`
	State         string   `json:"state"`
	StartedAtMS   int64    `json:"startedAtMs"`
	FinishedAtMS  int64    `json:"finishedAtMs,omitempty"`
	ResourceCount int      `json:"resourceCount"`
	Succeeded     int      `json:"succeeded"`
	Failed        int      `json:"failed"`
	NotQueried    int      `json:"notQueried"`
	Pending       int      `json:"pending"`
	Items         []Item   `json:"items"`
}

// Purpose is context, NOT source authority or a routing gate ACK.
type CompletedObservation struct {
	Purpose    Purpose
	Source     SourceRequest
	AttemptID  string
	Official   *ob.Status
	Credential *cq.Status
}
type Observer func(context.Context, CompletedObservation)

// Clock controls scheduling only; NEVER observation freshness.
type Clock interface {
	Now() time.Time
	Ticks() <-chan time.Time
}
