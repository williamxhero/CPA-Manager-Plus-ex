// Package credentialpolicy owns persistent per-credential intent and its
// independent Core restriction. A saved switch is never an admission ACK.
package credentialpolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const OwnerNamespace = "cpa-manager.credential-policy.v1/"

var ErrIdentity = errors.New("credential policy identity unavailable")
var ErrInput = errors.New("invalid credential policy request")
var ErrUnavailable = errors.New("credential policy gate unavailable")

type Update struct {
	cq.Binding
	AuthIndex          string `json:"auth_index"`
	SourceVersion      string `json:"sourceVersion"`
	ExpectedGeneration *int64 `json:"expectedGeneration"`
	Enabled            *bool  `json:"enabled"`
}

type Status struct {
	cq.Source
	Enabled           bool          `json:"enabled"`
	Generation        int64         `json:"generation"`
	Owner             string        `json:"owner"`
	Availability      rc.Capability `json:"availability"`
	QueryAvailability rc.Capability `json:"queryAvailability"`
	State             string        `json:"state"`
	Reason            string        `json:"reason"`
	Applied           bool          `json:"applied"`
}
type Service struct {
	Store     *store.Store
	Quota     *cq.Service
	core      coreClient
	lockedOff bool
}

func New(st *store.Store, quota *cq.Service, connection *mc.Service, lockedOff bool) *Service {
	return &Service{Store: st, Quota: quota, core: coreClient{connection: connection}, lockedOff: lockedOff}
}
func (s *Service) source(ctx context.Context, index string) (cq.Source, error) {
	sources, err := s.Quota.Sources(ctx)
	if err != nil {
		return cq.Source{}, ErrIdentity
	}
	for _, src := range sources {
		if index != "" && src.AuthIndex == index {
			return src, nil
		}
	}
	return cq.Source{}, ErrIdentity
}
func (s *Service) Status(ctx context.Context, index string) (Status, error) {
	src, err := s.source(ctx, index)
	if err != nil {
		return Status{}, err
	}
	return s.read(ctx, src)
}

func policyKey(src cq.Source) string {
	ids := []string{src.Scope.InstanceID, src.Scope.InstanceIncarnation}
	ts := append([]rc.Target(nil), src.Targets...)
	sort.Slice(ts, func(i, j int) bool { return ts[i].ResourceID < ts[j].ResourceID })
	for _, t := range ts {
		ids = append(ids, t.ResourceID, t.Incarnation)
	}
	b, _ := json.Marshal(ids)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func sameAuthority(a, b cq.Source) bool {
	a.Scope.BootEpoch = ""
	a.Scope.Revision = 0
	b.Scope.BootEpoch = ""
	b.Scope.Revision = 0
	a.AuthIndex = ""
	b.AuthIndex = ""
	a.ManualDisabled = false
	b.ManualDisabled = false
	a.State = ""
	b.State = ""
	a.Reason = ""
	b.Reason = ""
	return reflect.DeepEqual(a, b)
}
func (s *Service) read(ctx context.Context, src cq.Source) (Status, error) {
	installation, err := s.Store.CredentialPolicyInstallation(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{Source: src, Owner: OwnerNamespace + installation, State: "off", Reason: "gate_contract_unavailable", Availability: rc.Capability{State: "unavailable", Reason: "gate_contract_unavailable"}}
	out.QueryAvailability = rc.Capability{State: src.State, Reason: src.Reason}
	_, out.Reason = s.core.capabilities(ctx, src)
	if s.lockedOff {
		out.Reason = "environment_locked_off"
	}
	out.Availability.Reason = out.Reason
	r, err := s.Store.CredentialPolicy(ctx, policyKey(src))
	if err != nil {
		return Status{}, err
	}
	out.Generation = r.Generation
	if r.Document != "" {
		var saved Status
		if json.Unmarshal([]byte(r.Document), &saved) != nil {
			return Status{}, errors.New("credential policy storage unavailable")
		}
		if sameAuthority(saved.Source, src) {
			out.Enabled = saved.Enabled
		} else {
			out.Reason = "source_changed"
		}
	}
	// Capability/network I/O may span an instance switch or credential mutation.
	// A current selector is not a lease on its earlier issued source binding.
	current, verifyErr := s.source(ctx, src.AuthIndex)
	if verifyErr != nil || !reflect.DeepEqual(current, src) {
		return Status{}, ErrIdentity
	}
	return out, nil
}
func (s *Service) Update(ctx context.Context, q Update) (Status, error) {
	if q.Enabled == nil || q.ExpectedGeneration == nil || *q.ExpectedGeneration < 0 || *q.ExpectedGeneration >= 9007199254740991 {
		return Status{}, ErrInput
	}
	src, err := s.source(ctx, q.AuthIndex)
	if err != nil || !reflect.DeepEqual(src.Binding, q.Binding) || src.SourceVersion == "" || q.SourceVersion != src.SourceVersion {
		return Status{}, ErrIdentity
	}
	out, err := s.read(ctx, src)
	if err != nil {
		return Status{}, err
	}
	if out.Generation != *q.ExpectedGeneration {
		return Status{}, store.ErrPolicyConflict
	}
	if *q.Enabled {
		return Status{}, &UnavailableError{Reason: out.Availability.Reason}
	}
	out.Enabled = false
	out.Generation++
	document, err := json.Marshal(out)
	if err != nil {
		return Status{}, err
	}
	if err = s.Store.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: policyKey(src), Generation: out.Generation, Document: string(document)}, *q.ExpectedGeneration); err != nil {
		return Status{}, err
	}
	return out, nil
}

type UnavailableError struct{ Reason string }

func (e *UnavailableError) Error() string { return "credential policy unavailable" }
func (e *UnavailableError) Unwrap() error { return ErrUnavailable }
