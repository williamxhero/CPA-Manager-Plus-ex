package fullrefresh

import (
	"context"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

// These adapters preserve existing host wire responses while extending the join
// window through the full round. They never choose or infer query authority.
func (s *Service) StartOfficial(ctx context.Context, q ob.Query) (ob.Job, error) {
	request := SourceRequest{Kind: Official, Official: &q}
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
				item.Outcome = "running"
				s.purposes[job.ID] = p.Purpose
				_ = s.save(ctx, p)
			}
			return job, err
		}
	}
	return s.Official.Start(ctx, q)
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
				item.Outcome = "running"
				item.Request.Credential.Version = job.Query.Version
				s.purposes[job.ID] = p.Purpose
				_ = s.save(ctx, p)
			}
			return job, err
		}
	}
	return s.Credential.Start(ctx, q)
}
