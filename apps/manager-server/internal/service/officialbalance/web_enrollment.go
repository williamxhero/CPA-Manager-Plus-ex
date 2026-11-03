package officialbalance

import (
	"context"
	"regexp"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Registration never accepts verification booleans. A reference requests a
// review; it is not proof of an observed parser, account, profile or operator.
type WebEnrollmentUpdate struct {
	Binding
	Version             int64  `json:"version"`
	SourceAuthority     string `json:"sourceAuthority"`
	RegistrationVersion int64  `json:"registrationVersion"`
	ProofReference      string `json:"proofReference"`
	Withdraw            bool   `json:"withdraw"`
}

type WebEnrollmentStatus struct {
	Version              int64  `json:"version"`
	SourceAuthority      string `json:"sourceAuthority"`
	RegistrationVersion  int64  `json:"registrationVersion"`
	State                string `json:"state"`
	ProofReference       string `json:"proofReference,omitempty"`
	ParserSupported      bool   `json:"parserSupported"`
	CollectionAuthorized bool   `json:"collectionAuthorized"`
}

var enrollmentReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func (s *Service) webEnrollmentSource(ctx context.Context, b Binding) (Status, error) {
	if !s.verify(ctx, b) {
		return Status{}, ErrIdentity
	}
	status, err := s.read(ctx, b)
	if err != nil {
		return Status{}, err
	}
	if !status.Configured || status.Adapter != balance.LaysoBillingWebV1 {
		return Status{}, ErrInput
	}
	return status, nil
}

func (s *Service) readWebEnrollment(ctx context.Context, source Status) (WebEnrollmentStatus, error) {
	row, err := s.Store.OfficialBalanceWebEnrollment(ctx, key(source.Binding), source.Version)
	if err != nil {
		return WebEnrollmentStatus{}, err
	}
	out := WebEnrollmentStatus{Version: source.Version, SourceAuthority: source.WebSourceAuthority,
		RegistrationVersion: row.Revision, State: "not_registered",
		ParserSupported: s.WebReader != nil && s.WebReader.SupportsRule(source.Web.ParsingRule)}
	if row.SourceVersion == source.Version && row.Authority == source.WebSourceAuthority && row.ProofReference != "" {
		out.State = "pending_verification"
		out.ProofReference = row.ProofReference
	}
	return out, nil
}

func (s *Service) WebEnrollment(ctx context.Context, b Binding) (WebEnrollmentStatus, error) {
	source, err := s.webEnrollmentSource(ctx, b)
	if err != nil {
		return WebEnrollmentStatus{}, err
	}
	return s.readWebEnrollment(ctx, source)
}

func (s *Service) RegisterWebEnrollment(ctx context.Context, u WebEnrollmentUpdate) (WebEnrollmentStatus, error) {
	if u.Version <= 0 || u.Version >= 9007199254740991 || u.RegistrationVersion < 0 || u.RegistrationVersion >= 9007199254740991 ||
		len(u.SourceAuthority) != 64 || u.Withdraw && u.ProofReference != "" || !u.Withdraw && !enrollmentReference.MatchString(u.ProofReference) {
		return WebEnrollmentStatus{}, ErrInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.webEnrollmentSource(ctx, u.Binding)
	if err != nil {
		return WebEnrollmentStatus{}, err
	}
	if source.Version != u.Version || source.WebSourceAuthority != u.SourceAuthority {
		return WebEnrollmentStatus{}, store.ErrBalanceConflict
	}
	err = s.Store.SaveOfficialBalanceWebEnrollment(ctx, key(u.Binding), u.RegistrationVersion,
		store.WebEnrollmentRegistration{SourceVersion: source.Version, Authority: source.WebSourceAuthority, ProofReference: u.ProofReference})
	if err != nil {
		return WebEnrollmentStatus{}, err
	}
	return s.readWebEnrollment(ctx, source)
}
