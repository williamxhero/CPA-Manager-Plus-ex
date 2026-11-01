package fullrefresh

import (
	"context"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"time"
)

// These adapters preserve existing host wire responses while extending the join
// window through the full round. They never choose or infer query authority.
func (s *Service) StartOfficial(ctx context.Context, q ob.Query) (ob.Job, error) {
	request := SourceRequest{Kind: Official, Official: &q}
	var proof *WalletMembership
	var walletMembers []SourceStatus
	if status, err := s.Official.Status(ctx, q.Binding); err == nil {
		proof = verifiedWallet(status)
		if proof != nil {
			if inv, err := s.Inventory(ctx); err == nil {
				for _, member := range inv.Sources {
					if member.WalletProof != nil && member.WalletProof.GroupID == proof.GroupID {
						walletMembers = append(walletMembers, member)
					}
				}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ob.Job{}, ErrUnavailable
	}
	for _, p := range s.rounds {
		if p.State != "running" {
			continue
		}
		for i := range p.Items {
			item := &p.Items[i]
			if !requestSame(item.Request, request) {
				continue
			}
			if item.SharedFromID != "" {
				return ob.Job{}, ob.ErrBusy
			}
			if item.JobID != "" {
				job, err := s.Official.Job(ctx, item.JobID)
				if err == nil {
					item.Joined = true
					_ = s.save(ctx, p)
				}
				return job, err
			}
			job, err := s.Official.Start(ctx, q)
			if err == nil {
				item.JobID = job.ID
				item.Joined = true
				item.Queried = true
				s.markAdmitted(ctx, p, item)
				item.Outcome = "running"
				s.purposes[job.ID] = p.Purpose
				_ = s.save(ctx, p)
			}
			return job, err
		}
	}
	if err := s.ensureLocked(ctx); err != nil {
		return ob.Job{}, err
	}
	job, err := s.Official.Start(ctx, q)
	if err == nil {
		row := SourceStatus{WalletProof: proof, ID: sourceID(Official, q.Scope, q.Targets), Request: request, Targets: q.Targets, State: "running", QueryCapable: true, Queried: true}
		err = s.recordManual(ctx, job.ID, q.Scope, row, job.Joined, walletMembers...)
	}
	return job, err
}
func (s *Service) StartCredential(ctx context.Context, q cq.Query) (cq.Job, error) {
	request := SourceRequest{Kind: Credential, Credential: &q}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return cq.Job{}, ErrUnavailable
	}
	for _, p := range s.rounds {
		if p.State != "running" {
			continue
		}
		for i := range p.Items {
			item := &p.Items[i]
			if !requestSame(item.Request, request) {
				continue
			}
			if item.JobID != "" {
				job, err := s.Credential.Job(ctx, item.JobID)
				if err != nil {
					return job, err
				}
				if q.Version != job.RequestedVersion && q.Version != job.Query.Version {
					return cq.Job{}, store.ErrQuotaConflict
				}
				item.Joined = true
				_ = s.save(ctx, p)
				return job, nil
			}
			job, err := s.Credential.Start(ctx, q)
			if err == nil {
				item.JobID = job.ID
				item.Joined = true
				item.Queried = true
				s.markAdmitted(ctx, p, item)
				item.Outcome = "running"
				item.Request.Credential.Version = job.Query.Version
				s.purposes[job.ID] = p.Purpose
				_ = s.save(ctx, p)
			}
			return job, err
		}
	}
	if err := s.ensureLocked(ctx); err != nil {
		return cq.Job{}, err
	}
	job, err := s.Credential.Start(ctx, q)
	if err == nil {
		current := job.Query
		row := SourceStatus{ID: sourceID(Credential, q.Scope, q.Targets), Request: SourceRequest{Kind: Credential, Credential: &current}, Targets: q.Targets, State: "running", QueryCapable: true, Queried: true}
		err = s.recordManual(ctx, job.ID, q.Scope, row, job.Joined)
	}
	return job, err
}

// Caller holds the coordinator lock; host already durably accepted the actual job.
func (s *Service) recordManual(ctx context.Context, id string, scope rc.Scope, source SourceStatus, joined bool, members ...SourceStatus) error {
	if _, exists := s.rounds[id]; exists {
		return nil
	}
	if len(s.rounds) >= 128 {
		return ErrUnavailable
	}
	p := &Progress{ID: id, Purpose: Manual, Scope: scope, State: "running", StartedAtMS: time.Now().UnixMilli(), ResourceCount: len(source.Targets), Items: []Item{{SourceStatus: source, JobID: id, Outcome: "running", Joined: joined}}}
	for _, member := range members {
		if member.ID != source.ID {
			p.Items = append(p.Items, Item{SourceStatus: member, Outcome: "waiting_shared", SharedFromID: source.ID})
			p.ResourceCount += len(member.Targets)
		}
	}
	recount(p)
	if err := s.save(ctx, p); err != nil {
		return err
	}
	s.rounds[p.ID] = p
	if s.purposes[id] == "" {
		s.purposes[id] = Manual
	}
	return nil
}
