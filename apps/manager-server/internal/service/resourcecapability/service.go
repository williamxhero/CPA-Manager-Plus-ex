// Package resourcecapability reports manager-wide support, not resource bindings
// or evidence that an automatic policy has been applied by core.
package resourcecapability

import (
	"context"
	"errors"

	automationsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/automation"
)

type Capability struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type Status struct {
	SchemaVersion   int                  `json:"schemaVersion"`
	Identity        Capability           `json:"identity"`
	Query           Capability           `json:"query"`
	IndependentGate Capability           `json:"independentGate"`
	Policy          automationsvc.Status `json:"policy"`
}

type Service struct {
	policy *automationsvc.Service
}

func New(policy *automationsvc.Service) *Service {
	return &Service{policy: policy}
}

// Status reads only existing policy settings. It does not inspect a connection,
// probe upstreams, infer core support, or create persistent resource identities.
func (s *Service) Status(ctx context.Context) (Status, error) {
	policy, err := s.policy.Status(ctx)
	if err != nil {
		return Status{}, errors.New("resource capabilities unavailable")
	}
	return Status{
		SchemaVersion:   1,
		Identity:        Capability{State: "unknown", Reason: "core_contract_unverified"},
		Query:           Capability{State: "unknown", Reason: "query_contract_unverified"},
		IndependentGate: Capability{State: "unavailable", Reason: "consumer_contract_unconfigured"},
		Policy:          policy,
	}, nil
}
