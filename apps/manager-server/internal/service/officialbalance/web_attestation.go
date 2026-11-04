package officialbalance

import (
	"context"
	"errors"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// AttestWebBrowserHost records a trusted host-side Profile 1 ↔ instance mapping.
// Callers are host startup/tools/tests only — never management HTTP input.
func (s *Service) AttestWebBrowserHost(ctx context.Context, att store.WebBrowserHostAttestation) error {
	if s == nil || s.Store == nil {
		return ErrInput
	}
	if att.Family != "microsoft-edge" || att.Profile != "Profile 1" ||
		att.EvidenceKind != "verified_browser" && att.EvidenceKind != "controlled_fixture" ||
		!enrollmentReference.MatchString(att.ProofReference) ||
		!enrollmentReference.MatchString(att.InstanceID) ||
		!enrollmentReference.MatchString(att.ExecutionUser) {
		return ErrInput
	}
	if att.VerifiedAtMS <= 0 {
		att.VerifiedAtMS = time.Now().UnixMilli()
	}
	return s.Store.SaveWebBrowserHostAttestation(ctx, att)
}

func (s *Service) resolveWebEnrollment(q balance.WebRequest) (balance.WebEnrollment, bool) {
	if s == nil || s.Store == nil || balance.ValidateWebContract(&q.Source) != nil {
		return balance.WebEnrollment{}, false
	}
	att, err := s.Store.WebBrowserHostAttestation(context.Background(),
		q.Source.Browser.Family, q.Source.Browser.Profile, q.Source.Browser.InstanceID, q.Source.ExecutionUser)
	if err != nil {
		return balance.WebEnrollment{}, false
	}
	if att.Family != q.Source.Browser.Family || att.Profile != q.Source.Browser.Profile ||
		att.InstanceID != q.Source.Browser.InstanceID || att.ExecutionUser != q.Source.ExecutionUser ||
		att.ProofReference == "" || (att.EvidenceKind != "verified_browser" && att.EvidenceKind != "controlled_fixture") {
		return balance.WebEnrollment{}, false
	}
	// ParserVerified is granted only after BrowserSkill.SupportsRule already
	// admitted the host-installed rule; this callback never invents a parser.
	return balance.WebEnrollment{
		Authority:             q.Authority,
		Source:                q.Source,
		ProfileVerified:       true,
		ExecutionUserVerified: true,
		ParserVerified:        true,
		ProofReference:        att.ProofReference,
		EvidenceKind:          att.EvidenceKind,
	}, true
}

// RevokeWebBrowserHostAttestations clears host attestations (tests / host ops).
func (s *Service) RevokeWebBrowserHostAttestations(ctx context.Context) error {
	if s == nil || s.Store == nil {
		return errors.New("store unavailable")
	}
	return s.Store.ClearWebBrowserHostAttestations(ctx)
}
