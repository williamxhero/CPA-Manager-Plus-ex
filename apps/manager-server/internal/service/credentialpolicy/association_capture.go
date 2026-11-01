package credentialpolicy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	a "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy/association"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type officialCapture struct {
	Request    a.Request `json:"request"`
	Source     cq.Source `json:"source"`
	SubjectID  string    `json:"subjectId"`
	Generation int64     `json:"generation"`
	Owner      string    `json:"owner"`
}

func containsSelected(group ob.Binding, selected cq.Binding) bool {
	if group.Scope != selected.Scope || len(selected.Targets) == 0 {
		return false
	}
	for _, want := range selected.Targets {
		found := false
		for _, got := range group.Targets {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// CaptureOfficialQueryStart performs LOCAL storage work only, never a Core RPC,
// proof/secret resolution or second upstream acquisition. Missing current issued
// discovery leaves automation unavailable; it cannot delay independent billing.
// Discovery is populated by the existing explicit policy source inspection. Old
// boot/snapshot entries cannot be projected or guessed into current descriptors.
func (s *Service) CaptureOfficialQueryStart(ctx context.Context, q ob.Query, jobID string) error {
	sourceID := SubjectID(cq.Source{Binding: cq.Binding{Scope: q.Scope, Targets: q.Targets}})
	stored, err := s.Store.OfficialBalance(ctx, sourceID)
	if err != nil {
		return err
	}
	var config ob.Config
	if !stored.Configured || stored.Version != q.Version || json.Unmarshal([]byte(stored.Config), &config) != nil || config.Adapter != balance.NewAPITokenUsageV1 {
		return nil
	}
	if !sameAuthority(cq.Source{Binding: cq.Binding{Scope: config.Scope, Targets: config.Targets}}, cq.Source{Binding: cq.Binding{Scope: q.Scope, Targets: q.Targets}}) {
		return ErrIdentity
	}
	config.Binding = q.Binding
	if config.Version != stored.Version {
		return ErrIdentity
	}
	job, err := s.Store.OfficialBalanceJob(ctx, jobID)
	if err != nil {
		return err
	}
	var actual ob.Job
	if json.Unmarshal([]byte(job), &actual) != nil || actual.ID != jobID || actual.State != "running" || actual.Version != q.Version || !reflect.DeepEqual(actual.Binding, q.Binding) {
		return ErrIdentity
	}
	documents, err := s.Store.CredentialPolicySources(ctx)
	if err != nil {
		return err
	}
	installation, err := s.Store.CredentialPolicyInstallation(ctx)
	if err != nil {
		return err
	}
	for _, doc := range documents {
		var src cq.Source
		if json.Unmarshal([]byte(doc), &src) != nil {
			return ErrInput
		}
		if !containsSelected(q.Binding, src.Binding) {
			continue
		}
		subject := SubjectID(src)
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("official-association:"+jobID+":"+subject)).String()
		if _, err := s.Store.CredentialAssociationCapture(ctx, id); err == nil {
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		previous, err := s.Store.CredentialPolicy(ctx, subject)
		if err != nil {
			return err
		}
		owner := OwnerNamespace + installation
		if previous.Document != "" {
			state, err := s.StoredStatus(ctx, subject)
			if err != nil {
				return err
			}
			if state.Owner != owner || !sameAuthority(state.Source, src) {
				continue
			}
		}
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return ErrUnavailable
		}
		origin, err := a.CanonicalOrigin(config.Endpoint)
		if err != nil {
			return err
		}
		request := a.Request{Version: 1, Selected: src.Binding, AuthIndex: src.AuthIndex, SourceVersion: src.SourceVersion, Origin: origin, Official: config, QueryID: jobID, Nonce: base64.RawURLEncoding.EncodeToString(nonce)}
		if err := a.ValidateCapture(request); err != nil {
			return err
		}
		body, err := json.Marshal(officialCapture{Request: request, Source: src, SubjectID: subject, Generation: previous.Generation, Owner: owner})
		if err != nil {
			return err
		}
		capture := store.CredentialAssociationCapture{ID: id, JobID: jobID, SourceID: sourceID, ConfigVersion: stored.Version, Config: stored.Config, SecretRef: stored.SecretRef, PolicyID: subject, PolicyGeneration: previous.Generation, PolicyDocument: previous.Document, Job: job, Document: string(body)}
		if err := s.Store.PutCredentialAssociationCapture(ctx, capture); err != nil {
			return err
		}
	}
	return nil
}
