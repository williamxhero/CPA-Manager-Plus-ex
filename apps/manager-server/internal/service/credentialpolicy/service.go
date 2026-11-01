// Package credentialpolicy owns persistent per-credential intent and its
// independent Core restriction. A saved switch is never an admission ACK.
package credentialpolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"reflect"
	"sort"
	"sync"
	"time"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
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
	Owner              string `json:"owner,omitempty"`
}

type Status struct {
	cq.Source
	SubjectID          string           `json:"subjectId"`
	BindingCurrent     bool             `json:"bindingCurrent"`
	Enabled            bool             `json:"enabled"`
	Generation         int64            `json:"generation"`
	Owner              string           `json:"owner"`
	Availability       rc.Capability    `json:"availability"`
	QueryAvailability  rc.Capability    `json:"queryAvailability"`
	State              string           `json:"state"`
	Reason             string           `json:"reason"`
	Applied            bool             `json:"applied"`
	Operation          *OperationStatus `json:"operation,omitempty"`
	AuthorityReboundTo string           `json:"authorityReboundTo,omitempty"`
}

// SourceReader supplies the authoritative current inventory, not query results.
type SourceReader interface {
	Sources(context.Context) ([]cq.Source, error)
}

type Service struct {
	Store       *store.Store
	Quota       SourceReader
	core        coreClient
	lockedOff   bool
	reconcileMu sync.Mutex
	holder      string
	clock       func() time.Time
	coordinator *fr.Service
	runMu       sync.Mutex
	running     bool
	wake        chan struct{}
}

type Option func(*Service)

func WithClock(clock func() time.Time) Option {
	return func(s *Service) {
		if clock != nil {
			s.clock = clock
		}
	}
}
func (s *Service) now() time.Time { return s.clock().UTC() }
func New(st *store.Store, quota SourceReader, connection *mc.Service, lockedOff bool, options ...Option) *Service {
	s := &Service{Store: st, Quota: quota, core: coreClient{connection: connection}, lockedOff: lockedOff, holder: uuid.NewString(), clock: time.Now, wake: make(chan struct{}, 1)}
	for _, option := range options {
		option(s)
	}
	return s
}
func (s *Service) source(ctx context.Context, index string) (cq.Source, error) {
	if s.Quota == nil {
		return cq.Source{}, ErrIdentity
	}
	sources, err := s.Quota.Sources(ctx)
	if err != nil {
		return cq.Source{}, ErrIdentity
	}
	for _, src := range sources {
		if src.AuthIndex == "" || len(src.Targets) == 0 {
			continue
		}
		document, err := json.Marshal(src)
		if err != nil {
			return cq.Source{}, ErrIdentity
		}
		if err := s.Store.RememberCredentialPolicySource(ctx, SubjectID(src), string(document)); err != nil {
			return cq.Source{}, err
		}
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

func SubjectID(src cq.Source) string { return policyKey(src) }
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
	out.SubjectID = policyKey(src)
	out.BindingCurrent = true
	out.QueryAvailability = rc.Capability{State: src.State, Reason: src.Reason}
	_, out.Reason = s.core.capabilities(ctx, src)
	if s.lockedOff {
		out.Reason = "environment_locked_off"
	}
	out.Availability.Reason = out.Reason
	if out.Reason == "" {
		out.Availability.State = "available"
		out.Availability.Reason = "current_source_gate_ready"
	}
	r, err := s.Store.CredentialPolicy(ctx, policyKey(src))
	if err != nil {
		return Status{}, err
	}
	r, err = s.certifiedIntent(ctx, src, r)
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
			if out.Enabled {
				out.State = "armed"
			}
		} else {
			out.Reason = "source_changed"
		}
	}
	if out.Reason == "" && src.State != "ready" {
		if _, err := s.officialPrerequisite(ctx, out); err != nil {
			out.Reason = "verified_selected_key_evidence_required"
			var unavailable *UnavailableError
			if errors.As(err, &unavailable) {
				out.Reason = unavailable.Reason
			}
			out.Availability = rc.Capability{State: "unavailable", Reason: out.Reason}
		} else {
			out.QueryAvailability = rc.Capability{State: "available", Reason: "verified_selected_key_evidence"}
		}
	}
	if err := s.attachOperation(ctx, &out, true); err != nil {
		return Status{}, err
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
	if !*q.Enabled {
		out, known, err := s.localOff(ctx, q)
		if known || err != nil {
			return out, err
		}
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
	if q.Owner != "" && q.Owner != out.Owner {
		return Status{}, ErrIdentity
	}
	var prerequisite store.CredentialPolicyQueryFence
	var completed string
	var associationEvidence store.CredentialAssociationEvidence
	if *q.Enabled {
		if out.Availability.State != "available" {
			return Status{}, &UnavailableError{Reason: out.Availability.Reason}
		}
		if src.State == "ready" {
			prerequisite, completed, err = s.enablePrerequisite(ctx, out)
		} else {
			associationEvidence, err = s.officialPrerequisite(ctx, out)
		}
		if err != nil {
			return Status{}, err
		}
		out.Enabled = true
		out.State = "armed"
		out.Reason = "policy_enabled"
	} else {
		out.Enabled = false
		out.State = "off"
	}
	out.Generation++
	document, err := json.Marshal(out)
	if err != nil {
		return Status{}, err
	}
	record := store.CredentialPolicyRecord{ID: policyKey(src), Generation: out.Generation, Document: string(document)}
	if *q.Enabled {
		record.RefreshScheduleKey, err = s.enableScheduleKey(ctx, prerequisite, associationEvidence)
		if err != nil {
			return Status{}, err
		}
		if src.State == "ready" {
			err = s.Store.SaveCredentialPolicyWithPrerequisite(ctx, record, *q.ExpectedGeneration, prerequisite, completed)
		} else {
			err = s.Store.SaveCredentialPolicyWithAssociation(ctx, record, *q.ExpectedGeneration, associationEvidence)
		}
	} else {
		err = s.Store.SaveCredentialPolicy(ctx, record, *q.ExpectedGeneration)
	}
	if err != nil {
		return Status{}, err
	}
	return out, nil
}

// StoredStatus is explicitly a local intent read, not current Core authority.
// A previously issued subject handle is useful for emergency OFF while Core is
// unavailable; it cannot enroll a new subject or authorize ON/resume.
func (s *Service) StoredStatus(ctx context.Context, id string) (Status, error) {
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 32 {
		return Status{}, ErrInput
	}
	r, err := s.Store.CredentialPolicy(ctx, id)
	if err != nil {
		return Status{}, err
	}
	if r.Document == "" {
		return Status{}, ErrIdentity
	}
	var out Status
	if json.Unmarshal([]byte(r.Document), &out) != nil || policyKey(out.Source) != id || out.Generation != r.Generation {
		return Status{}, errors.New("credential policy storage unavailable")
	}
	installation, err := s.Store.CredentialPolicyInstallation(ctx)
	if err != nil {
		return Status{}, err
	}
	if out.Owner != OwnerNamespace+installation {
		return Status{}, ErrIdentity
	}
	out.SubjectID = id
	out.BindingCurrent = false
	out.Applied = false
	out.Availability = rc.Capability{State: "unavailable", Reason: "current_binding_unverified"}
	out.Reason = "current_binding_unverified"
	if !out.Enabled {
		out.State = "off"
	} else {
		out.State = "pending"
	}
	if err := s.attachOperation(ctx, &out, false); err != nil {
		return Status{}, err
	}
	return out, nil
}

func (s *Service) localOff(ctx context.Context, q Update) (Status, bool, error) {
	id := policyKey(cq.Source{Binding: q.Binding})
	r, err := s.Store.CredentialPolicy(ctx, id)
	if err != nil {
		return Status{}, true, err
	}
	if r.Document == "" {
		return Status{}, false, nil
	}
	out, err := s.StoredStatus(ctx, id)
	if err != nil {
		return Status{}, true, err
	}
	if out.Generation != *q.ExpectedGeneration {
		return Status{}, true, store.ErrPolicyConflict
	}
	if !reflect.DeepEqual(out.Binding, q.Binding) || out.SourceVersion != q.SourceVersion || q.Owner != "" && q.Owner != out.Owner {
		return Status{}, true, ErrIdentity
	}
	out.Enabled = false
	out.State = "off"
	out.Reason = "local_intent_off"
	out.Generation++
	document, err := json.Marshal(out)
	if err != nil {
		return Status{}, true, err
	}
	err = s.Store.SaveCredentialPolicy(ctx, store.CredentialPolicyRecord{ID: id, Generation: out.Generation, Document: string(document)}, *q.ExpectedGeneration)
	if err == nil {
		err = s.attachOperation(ctx, &out, false)
	}
	return out, true, err
}

type UnavailableError struct{ Reason string }

func (e *UnavailableError) Error() string { return "credential policy unavailable" }
func (e *UnavailableError) Unwrap() error { return ErrUnavailable }
