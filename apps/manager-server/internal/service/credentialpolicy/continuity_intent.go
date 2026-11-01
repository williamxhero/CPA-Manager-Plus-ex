package credentialpolicy

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// Current producer certification supports only single-target native refresh,
// preserving issued resource_id and membership. This narrows SEARCH ONLY; the
// exact sealed full-version Core certificate remains mandatory for authority.
func refreshCandidate(previous, current cq.Binding) bool {
	return len(previous.Targets) == 1 && len(current.Targets) == 1 && previous.Scope.InstanceID == current.Scope.InstanceID && previous.Scope.InstanceIncarnation == current.Scope.InstanceIncarnation && previous.Targets[0].ResourceID == current.Targets[0].ResourceID && previous.Targets[0].MembershipVersion == current.Targets[0].MembershipVersion
}

// Only the Core's sealed exact-target certificate can carry an EXISTING intent.
// No credential-version arithmetic, file/name/index/account-label inference.
// Old evidence/fences are discarded, never promoted to the rebound generation.
func (s *Service) certifiedIntent(ctx context.Context, current cq.Source, record store.CredentialPolicyRecord) (store.CredentialPolicyRecord, error) {
	if record.Document != "" {
		var saved Status
		if json.Unmarshal([]byte(record.Document), &saved) != nil {
			return record, ErrInput
		}
		if sameAuthority(saved.Source, current) || saved.AuthorityReboundTo != "" {
			return record, nil
		}
	}
	records, err := s.Store.CredentialPolicies(ctx)
	if err != nil {
		return record, err
	}
	installation, err := s.Store.CredentialPolicyInstallation(ctx)
	if err != nil {
		return record, err
	}
	// Current subject is a preference only, NOT a certificate or eligibility rule.
	sort.SliceStable(records, func(i, j int) bool { return records[i].ID == record.ID && records[j].ID != record.ID })
	var previous *store.CredentialPolicyRecord
	var original Status
	for _, candidate := range records {
		var saved Status
		if json.Unmarshal([]byte(candidate.Document), &saved) != nil {
			return record, ErrInput
		}
		if saved.AuthorityReboundTo != "" || saved.Owner != OwnerNamespace+installation || SubjectID(saved.Source) != candidate.ID || saved.Generation != candidate.Generation || !refreshCandidate(saved.Binding, current.Binding) || saved.Protocol != current.Protocol || saved.SourceVersion != current.SourceVersion {
			continue
		}
		if sameAuthority(saved.Source, current) {
			continue
		}
		proven, _ := s.core.continuity(ctx, saved.Binding, current)
		if !proven {
			if ctx.Err() != nil {
				return record, ctx.Err()
			}
			continue
		}
		if previous != nil {
			return record, ErrIdentity
		} // two predecessor intents are ambiguous
		copy := candidate
		previous = &copy
		original = saved
	}
	if previous == nil {
		return record, nil
	}
	// Never overwrite an independent intent already explicitly saved at a new ID.
	if record.Document != "" && record.ID != previous.ID {
		return record, store.ErrPolicyConflict
	}
	after, err := s.source(ctx, current.AuthIndex)
	if err != nil || !reflect.DeepEqual(after, current) {
		return record, ErrIdentity
	}
	next := Status{Source: current, SubjectID: SubjectID(current), BindingCurrent: true, Enabled: original.Enabled, Generation: previous.Generation + 1, Owner: original.Owner, State: "off", Reason: "fresh_authoritative_evidence_required"}
	if next.Enabled {
		next.State = "armed"
	}
	doc, err := json.Marshal(next)
	if err != nil {
		return record, err
	}
	tomb := original
	tomb.Enabled = false
	tomb.Generation = next.Generation
	tomb.State = "off"
	tomb.Reason = "certified_authority_rebound"
	tomb.Applied = false
	tomb.Operation = nil
	tomb.AuthorityReboundTo = next.SubjectID
	tombDoc, err := json.Marshal(tomb)
	if err != nil {
		return record, err
	}
	updated := store.CredentialPolicyRecord{ID: next.SubjectID, Generation: next.Generation, Document: string(doc)}
	if err := s.Store.MigrateCredentialPolicyAuthority(ctx, *previous, updated, string(tombDoc)); err != nil {
		return record, err
	}
	return updated, nil
}
