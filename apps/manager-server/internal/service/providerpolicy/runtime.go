package providerpolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	fr "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	pg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type captured struct {
	Binding Binding  `json:"binding"`
	Query   ob.Query `json:"query"`
}
type completion struct {
	AttemptID string   `json:"attemptId"`
	Query     ob.Query `json:"query"`
}
type decision struct {
	Binding         Binding         `json:"binding"`
	Action          string          `json:"action"`
	Evidence        string          `json:"evidence"`
	GateGeneration  int64           `json:"gateGeneration"`
	SourceVersion   string          `json:"sourceVersion"`
	Generation      int64           `json:"generation"`
	AttemptID       string          `json:"attemptId"`
	EvidenceSources []FundingStatus `json:"evidenceSources"`
}

func containsSource(b Binding, q ob.Query) bool {
	if b.Scope != q.Scope || len(q.Targets) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, t := range q.Targets {
		found := false
		for _, target := range b.Targets {
			found = found || target == t
		}
		if !found || seen[t.ResourceID] {
			return false
		}
		seen[t.ResourceID] = true
	}
	return true
}

func (s *Service) capture(ctx context.Context, event fr.StartedObservation) error {
	if event.Source.Kind != fr.Official || event.Source.Official == nil {
		return nil
	}
	policies, err := s.Store.ProviderPolicies(ctx)
	if err != nil {
		return err
	}
	for _, r := range policies {
		var saved intent
		if json.Unmarshal([]byte(r.Document), &saved) != nil {
			return ErrUnavailable
		}
		if !saved.Enabled {
			continue
		}
		b, _, err := s.binding(ctx, saved.ProviderID)
		if err != nil {
			continue
		}
		if !sameAuthority(saved.Binding, b) || !containsSource(b, *event.Source.Official) {
			continue
		}
		doc, _ := json.Marshal(captured{b, *event.Source.Official})
		if err := s.Store.CaptureProviderPolicy(ctx, store.ProviderPolicyCapture{SubjectID: r.ID, Generation: r.Generation, AttemptID: event.AttemptID, Document: string(doc)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) completed(ctx context.Context, event fr.CompletedObservation) {
	if event.Source.Kind != fr.Official || event.Source.Official == nil {
		return
	}
	doc, _ := json.Marshal(completion{event.AttemptID, *event.Source.Official})
	if s.Store.PutProviderPolicyEvent(ctx, event.AttemptID, string(doc)) != nil {
		return
	}
	// The coordinator shares a five-second notification budget across all
	// observers. Persist and wake only: authenticated proof/dispatch belongs to
	// the independently bounded runtime, not to a source completion callback.
	s.wakeWorker()
}

// Reconcile is scheduler/lifecycle work, never a side effect of a status GET.
// Failed or lost receipts remain pending; no intent is displayed as applied.
func (s *Service) Reconcile(ctx context.Context) error {
	// Every operation's context expires before the 30-second lease. This also
	// fences an in-progress Core exchange if a slow source exhausts the budget.
	ctx, cancelBudget := context.WithTimeout(ctx, 15*time.Second)
	defer cancelBudget()
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()

	lease, err := s.Store.AcquireCredentialPolicyLease(ctx, s.holder, time.Now().UnixMilli(), 30000)
	if err != nil {
		return err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Store.ReleaseCredentialPolicyLease(release, lease)
	}()
	// Recover only from already persisted actual jobs, not cached timestamps.
	captures, err := s.Store.ProviderPolicyUnfinishedCaptures(ctx)
	if err != nil {
		return err
	}
	for _, capture := range captures {
		var c captured
		if json.Unmarshal([]byte(capture.Document), &c) != nil {
			return ErrUnavailable
		}
		job, err := s.Store.OfficialBalanceJob(ctx, capture.AttemptID)
		if err != nil {
			continue
		}
		var result ob.Job
		if json.Unmarshal([]byte(job), &result) != nil || result.State == "running" {
			continue
		}
		doc, _ := json.Marshal(completion{capture.AttemptID, c.Query})
		if err := s.Store.PutProviderPolicyEvent(ctx, capture.AttemptID, string(doc)); err != nil {
			return err
		}
	}
	work, err := s.Store.ProviderPolicyFrontier(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range work {
		if err := s.Store.MarkProviderPolicyAttempt(ctx, item.Kind+"/"+item.ID, lease, time.Now().UnixMilli()); err != nil {
			return err
		}
		// A full current-key proof and dispatch may require several authenticated
		// Core reads. Do not restart that chain at a fixed five-second deadline:
		// under ordinary management latency it would never reach the gate POST.
		// The caller's 15-second lifecycle budget (or the scheduler's smaller
		// reserved child budget) still bounds all I/O and lock/lease ownership.
		attempt, cancel := context.WithCancel(ctx)
		var e error
		if item.Kind == "policy" {
			var saved intent
			if json.Unmarshal([]byte(item.Document), &saved) != nil || saved.Pending == nil {
				cancel()
				return ErrUnavailable
			}
			e = s.reconcilePending(attempt, store.ProviderPolicyRecord{ID: item.ID, Generation: item.Generation, Document: item.Document}, saved, lease)
		} else {
			var finished completion
			if json.Unmarshal([]byte(item.Document), &finished) != nil {
				cancel()
				return ErrUnavailable
			}
			e = s.consume(attempt, finished, lease)
		}
		cancel()
		if e != nil {
			failures = append(failures, e)
			continue
		}
		if item.Kind == "event" {
			if err := s.Store.CompleteProviderPolicyEvent(ctx, item.ID); err != nil {
				return err
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) reconcilePending(ctx context.Context, r store.ProviderPolicyRecord, saved intent, lease store.CredentialPolicyLease) error {
	d := saved.Pending
	b, _, err := s.binding(ctx, saved.ProviderID)
	if err != nil {
		return err
	}
	gate, err := s.gate.Status(ctx, pg.Binding(b))
	if err != nil {
		return err
	}
	retire := func(outcome string) error {
		saved.Pending = nil
		raw, _ := json.Marshal(saved)
		return s.Store.ReviseProviderPolicy(ctx, r, string(raw), lease, time.Now().UnixMilli(), outcome)
	}
	if gate.Applied && gate.Action == d.Action && gate.EvidenceID == "balance/"+d.Evidence {
		return retire("confirmed_applied")
	}
	unsent := gate.Generation == d.GateGeneration
	obsolete := !reflect.DeepEqual(d.Binding, b)
	if !saved.Enabled || d.Generation != r.Generation || obsolete {
		if unsent || gate.Settled && gate.State == "rejected" {
			return retire("retired_unattempted_or_obsolete")
		}
		return nil
	}
	if gate.Generation != d.GateGeneration && gate.Settled && gate.State == "rejected" {
		d.GateGeneration = gate.Generation
		raw, _ := json.Marshal(saved)
		if err := s.Store.ReviseProviderPolicy(ctx, r, string(raw), lease, time.Now().UnixMilli()); err != nil {
			return err
		}
		r.Document = string(raw)
		unsent = true
	}
	view, err := s.funding(ctx, b)
	if err != nil {
		return err
	}
	valid := false
	for _, src := range view.sources {
		if src.QueryID == d.AttemptID && src.KeyMatched && src.Fresh && (d.Action == "pause" && src.low || d.Action == "clear" && src.high) {
			valid = true
		}
		if d.Action == "pause" && src.KeyMatched && src.low {
			capture, e := s.Store.ProviderPolicyCapture(ctx, r.ID, src.QueryID)
			if e != nil || capture.Generation != r.Generation {
				valid = false
				break
			}
		}
	}
	if !valid || d.Action == "pause" && !view.allLow {
		if unsent {
			return retire("retired_unattempted_or_obsolete")
		}
		// The immutable operation may have been accepted. Do not replay a
		// first-commit clear after contrary, expired, or replaced money evidence.
		return nil
	}
	if !unsent {
		if !gate.Settled {
			if err := s.Store.CheckCredentialPolicyLease(ctx, lease, time.Now().UnixMilli()); err != nil {
				return err
			}
			_, err = s.gate.ReconcileEvidence(ctx, pg.Binding(b), s.currentEvidence(r, view.sources, lease))
			return err
		}
		return nil
	}
	if err := s.Store.CheckCredentialPolicyLease(ctx, lease, time.Now().UnixMilli()); err != nil {
		return err
	}
	_, err = s.gate.UpdateEvidence(ctx, pg.Update{Binding: pg.Binding(b), Version: 1, ExpectedGeneration: &d.GateGeneration, SourceVersion: d.SourceVersion, Action: d.Action}, d.Evidence, s.currentEvidence(r, view.sources, lease))
	return err
}

// Re-read only local authority after gate preparation's last remote read.
// The authenticated inventory is the gate's current, closed envelope; source
// versions and actual observation IDs may have changed independently of it.
// No network proof is cached and no source failure can turn into a recovery.
func (s *Service) currentEvidence(record store.ProviderPolicyRecord, sources []FundingStatus, lease store.CredentialPolicyLease) pg.EvidenceGuard {
	return func(ctx context.Context, inventory *rc.Negotiated) error {
		if err := s.Store.CheckCredentialPolicyLease(ctx, lease, time.Now().UnixMilli()); err != nil {
			return err
		}
		current, err := s.Store.ProviderPolicy(ctx, record.ID)
		if err != nil {
			return err
		}
		if current.Generation != record.Generation || current.Document != record.Document {
			return ErrUnavailable
		}
		fences := make([]store.OfficialBalanceFence, 0, len(sources))
		for _, source := range sources {
			status, fence, err := s.official.InventoryStatusEvidence(ctx, source.Query.Binding, inventory)
			if err != nil || !status.Configured || status.Version != source.Query.Version {
				return ErrUnavailable
			}
			fences = append(fences, fence)
			if !source.KeyMatched {
				continue
			}
			low, high := monetary(status, time.Now())
			if status.LastSuccess == nil && source.QueryID != "" || status.LastSuccess != nil && status.LastSuccess.QueryID != source.QueryID || low != source.low || high != source.high {
				return ErrUnavailable
			}
		}
		if err := s.Store.ValidateProviderPolicyEvidence(ctx, record, lease, time.Now().UnixMilli(), fences); err != nil {
			return ErrUnavailable
		}
		return nil
	}
}

func (s *Service) consume(ctx context.Context, event completion, lease store.CredentialPolicyLease) error {
	policies, err := s.Store.ProviderPolicies(ctx)
	if err != nil {
		return err
	}
	for _, record := range policies {
		var saved intent
		if json.Unmarshal([]byte(record.Document), &saved) != nil {
			return ErrUnavailable
		}
		if !saved.Enabled {
			continue
		}
		capture, err := s.Store.ProviderPolicyCapture(ctx, record.ID, event.AttemptID)
		if store.IsProviderPolicyCaptureMissing(err) {
			continue
		}
		if err != nil {
			return err
		}
		if capture.Generation != record.Generation {
			continue
		}
		if saved.Pending != nil {
			return ErrUnavailable
		}
		var c captured
		if json.Unmarshal([]byte(capture.Document), &c) != nil {
			return ErrUnavailable
		}
		if !reflect.DeepEqual(c.Query, event.Query) {
			continue
		}
		b, _, err := s.binding(ctx, saved.ProviderID)
		if err != nil {
			return err
		}
		if !sameAuthority(saved.Binding, b) || !reflect.DeepEqual(c.Binding, b) {
			continue
		}
		view, err := s.funding(ctx, b)
		if err != nil {
			return err
		}
		triggerFresh, triggerHigh := false, false
		allLow := view.allLow
		for _, source := range view.sources {
			if source.QueryID == event.AttemptID && sameSourceBinding(source.Query.Binding, event.Query.Binding) && source.Query.Version == event.Query.Version && source.KeyMatched && source.Fresh {
				triggerFresh = true
				triggerHigh = source.high
			}
			if source.KeyMatched && source.low {
				fence, e := s.Store.ProviderPolicyCapture(ctx, record.ID, source.QueryID)
				if e != nil || fence.Generation != record.Generation {
					allLow = false
				}
			}
		}
		if !triggerFresh {
			continue
		}
		action := ""
		if triggerHigh && view.gate.OwnPaused {
			action = "clear"
		} else if allLow && !view.gate.OwnPaused {
			action = "pause"
		}
		if action != "" {
			raw, _ := json.Marshal([]any{record.ID, record.Generation, event, view.gate.SourceVersion, view.sources})
			sum := sha256.Sum256(raw)
			saved.Pending = &decision{Binding: b, Action: action, Evidence: hex.EncodeToString(sum[:]), GateGeneration: view.gate.Generation, SourceVersion: view.gate.SourceVersion, Generation: record.Generation, AttemptID: event.AttemptID, EvidenceSources: append([]FundingStatus(nil), view.sources...)}
		}
		raw, _ := json.Marshal(saved)
		if err := s.Store.ReviseProviderPolicy(ctx, record, string(raw), lease, time.Now().UnixMilli()); err != nil {
			return err
		}
		if saved.Pending != nil {
			record.Document = string(raw)
			if err := s.reconcilePending(ctx, record, saved, lease); err != nil {
				return err
			}
		}
	}
	return nil
}
