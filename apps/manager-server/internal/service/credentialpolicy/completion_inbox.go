package credentialpolicy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type classifiedCompletion struct {
	Kind           fr.Kind          `json:"kind"`
	Query          cq.Query         `json:"query"`
	Official       *ob.Query        `json:"official,omitempty"`
	Source         cq.Source        `json:"source"`
	Decision       EvidenceDecision `json:"decision"`
	SubjectID      string           `json:"subjectId,omitempty"`
	Generation     int64            `json:"generation,omitempty"`
	Owner          string           `json:"owner,omitempty"`
	Witness        string           `json:"witness"`
	AssociationIDs []string         `json:"associationIds,omitempty"`
}

// ObserveCompleted commits a compact candidate before returning to76. It never
// waits for a gate RPC/lease/reconcile mutex and never performs another query.
func (s *Service) ObserveCompleted(ctx context.Context, event fr.CompletedObservation) error {
	raw, err := json.Marshal(struct {
		Source     fr.SourceRequest
		Credential *cq.Status
		Official   *ob.Status
	}{event.Source, event.Credential, event.Official})
	if err != nil || len(raw) > 4<<20 {
		return ErrInput
	}
	digest := sha256.Sum256(raw)
	witness := hex.EncodeToString(digest[:])
	old, err := s.Store.CredentialPolicyEvent(ctx, event.AttemptID)
	if err == nil {
		var previous classifiedCompletion
		if json.Unmarshal([]byte(old.Document), &previous) != nil || previous.Witness != witness {
			return store.ErrPolicyOperationConflict
		}
		return nil // never recapture a newer intent generation for an old attempt
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	candidate := classifiedCompletion{Kind: event.Source.Kind, Witness: witness}
	switch event.Source.Kind {
	case fr.Credential:
		if event.Source.Credential == nil || event.Credential == nil {
			return ErrInput
		}
		state, decision, err := s.classifyCompletion(ctx, *event.Source.Credential, *event.Credential, event.AttemptID)
		if err != nil {
			return err
		}
		if state.Owner == "" {
			return nil
		}
		candidate.Query, candidate.Source, candidate.Decision = *event.Source.Credential, event.Credential.Source, decision
		candidate.SubjectID, candidate.Generation, candidate.Owner = state.SubjectID, state.Generation, state.Owner
	case fr.Official:
		if event.Source.Official == nil || event.Official == nil || event.Official.Attempt == nil || event.Official.Attempt.QueryID != event.AttemptID {
			return ErrInput
		}
		candidate.Official = event.Source.Official
		candidate.Decision = noEvidence("credential_association_unverified")
		captures, err := s.Store.CredentialAssociationCapturesForJob(ctx, event.AttemptID)
		if err != nil {
			return err
		}
		for _, capture := range captures {
			var captured officialCapture
			if json.Unmarshal([]byte(capture.Document), &captured) != nil {
				return ErrInput
			}
			if captured.Request.Official.Version != event.Source.Official.Version || !reflect.DeepEqual(captured.Request.Official.Binding, event.Source.Official.Binding) {
				continue
			}
			candidate.AssociationIDs = append(candidate.AssociationIDs, capture.ID)
		}
	default:
		return ErrInput
	}
	document, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	if err := s.Store.PutCredentialPolicyEvent(ctx, store.CredentialPolicyEvent{ID: event.AttemptID, Document: string(document)}); err != nil {
		return err
	}
	s.wakeWorker()
	return nil
}

func (s *Service) ProcessCompletions(ctx context.Context) error {
	events, err := s.Store.PendingCredentialPolicyEvents(ctx)
	if err != nil {
		return err
	}
	for _, event := range events {
		var candidate classifiedCompletion
		if json.Unmarshal([]byte(event.Document), &candidate) != nil {
			return ErrInput
		}
		finish := func(reason string) error { return s.Store.FinishCredentialPolicyEvent(ctx, event.ID, reason) }
		if candidate.Kind == fr.Official {
			reason := "association_capture_unavailable"
			for _, id := range candidate.AssociationIDs {
				evaluation, err := s.EvaluateOfficialAssociation(ctx, id)
				if err != nil {
					return err
				}
				reason = evaluation.Reason
				if evaluation.Associated {
					capture, err := s.Store.CredentialAssociationCapture(ctx, id)
					if err != nil {
						return err
					}
					if capture.PolicyDocument == "" {
						reason = "policy_off"
						continue
					}
					state, err := s.StoredStatus(ctx, capture.PolicyID)
					if err != nil {
						return err
					}
					if !state.Enabled || s.lockedOff {
						reason = "policy_off"
						continue
					}
					rows, err := s.Store.CredentialAssociationEvidenceForPolicy(ctx, capture.PolicyID)
					if err != nil {
						return err
					}
					reason = "native_key_value_unknown"
					for _, row := range rows {
						if row.CaptureID != id {
							continue
						}
						var accepted acceptedAssociation
						if json.Unmarshal([]byte(row.Document), &accepted) != nil {
							return ErrInput
						}
						if accepted.Decision.Action == "none" || accepted.Decision.ValidUntilMS <= s.now().UnixMilli() {
							break
						}
						if _, err := s.enqueueDecision(ctx, state, accepted.Source, accepted.Decision); err != nil {
							return err
						}
						reason = "journaled"
						break
					}
				}
			}
			if err := finish(reason); err != nil {
				return err
			}
			continue
		}
		if candidate.Decision.Action == "none" {
			if err := finish(candidate.Decision.Reason); err != nil {
				return err
			}
			continue
		}
		state, err := s.StoredStatus(ctx, candidate.SubjectID)
		if err != nil {
			return err
		}
		if !state.Enabled || s.lockedOff || state.Generation != candidate.Generation || state.Owner != candidate.Owner {
			if err := finish("policy_generation_changed"); err != nil {
				return err
			}
			continue
		}
		if candidate.Decision.ValidUntilMS <= s.now().UnixMilli() {
			if err := finish("fresh_authoritative_evidence_required"); err != nil {
				return err
			}
			continue
		}
		query, _ := json.Marshal(candidate.Query)
		fence := store.CredentialPolicyQueryFence{AttemptID: event.ID, SubjectID: candidate.SubjectID, Generation: candidate.Generation, Owner: candidate.Owner, Source: string(query), Enabled: true}
		if err := s.Store.CheckCredentialPolicyQuery(ctx, fence); err != nil {
			if !errors.Is(err, store.ErrPolicyConflict) {
				return err
			}
			if err := finish("policy_generation_changed"); err != nil {
				return err
			}
			continue
		}
		source, err := s.source(ctx, candidate.Query.AuthIndex)
		if err != nil {
			continue
		} // keep pending until fresh current authority is available
		if !reflect.DeepEqual(source.Binding, candidate.Query.Binding) || source.SourceVersion != candidate.Query.SourceVersion || !sameAuthority(source, candidate.Source) || !sameAuthority(state.Source, source) {
			if err := finish("source_authority_changed"); err != nil {
				return err
			}
			continue
		}
		_, err = s.enqueueDecision(ctx, state, candidate.Source, candidate.Decision)
		if errors.Is(err, store.ErrPolicyLease) {
			continue
		}
		if errors.Is(err, store.ErrPolicyConflict) {
			if err := finish("policy_generation_changed"); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := finish("journaled"); err != nil {
			return err
		}
	}
	return nil
}
