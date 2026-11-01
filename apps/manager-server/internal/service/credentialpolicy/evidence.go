package credentialpolicy

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/google/uuid"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
)

// A candidate is not a gate action. Current generation/start fence, real source
// eligibility, durable journal and applied/current-ledger readback are separate.
type EvidenceDecision struct {
	Action                string `json:"action"`
	Model                 string `json:"model"`
	Reason                string `json:"reason"`
	EvidenceVersion       string `json:"evidenceVersion,omitempty"`
	AttemptID             string `json:"attemptId,omitempty"`
	ObservedAtMS          int64  `json:"observedAtMs,omitempty"`
	ModelAutomationReason string `json:"modelAutomationReason,omitempty"`
}

func noEvidence(reason string) EvidenceDecision {
	return EvidenceDecision{Action: "none", Reason: reason}
}

func ValidateCredentialEvidence(status cq.Status, now time.Time) EvidenceDecision {
	if status.Stale || status.State != "ready" || status.CredentialQuery.State != "available" || status.Attempt == nil || status.LastSuccess == nil || status.Attempt.QueryStatus != "success" || status.LastSuccess.QueryID != status.Attempt.QueryID {
		return noEvidence("fresh_authoritative_evidence_required")
	}
	obs := status.Attempt
	if id, err := uuid.Parse(obs.QueryID); err != nil || id.String() != obs.QueryID {
		return noEvidence("invalid_evidence")
	}
	if digest, err := hex.DecodeString(status.SourceVersion); err != nil || len(digest) != 32 {
		return noEvidence("source_authority_unverified")
	}
	var core cq.CoreObservation
	var binding cq.Binding
	if len(obs.WindowsJSON) > 2<<20 || json.Unmarshal([]byte(obs.WindowsJSON), &core) != nil || json.Unmarshal(obs.Binding, &binding) != nil || !reflect.DeepEqual(binding, status.Binding) || !reflect.DeepEqual(core.Binding, status.Binding) || core.SourceVersion != status.SourceVersion || core.Protocol != status.Protocol || core.AccountProvenance != status.AccountProvenance {
		return noEvidence("source_authority_changed")
	}
	if core.ProtocolVersion != 1 || core.QueryStatus != "success" || !core.Complete || len(core.Windows) == 0 || len(core.Windows) > 128 || core.TimestampProvenance != "upstream_http_date" || obs.ObservedAtMS != core.ObservedAtMS || core.ObservedAtMS <= 0 || core.ObservedAtMS > now.Add(time.Minute).UnixMilli() || core.ObservedAtMS < now.Add(-5*time.Minute).UnixMilli() || core.ReceivedAtMS < core.ObservedAtMS-60000 || core.ReceivedAtMS > now.Add(time.Minute).UnixMilli() {
		return noEvidence("fresh_authoritative_evidence_required")
	}
	if core.Protocol == "codex-oauth-wham" && core.AccountProvenance != "selected_oauth_account" || core.Protocol == "opencode-go-usage" && core.AccountProvenance != "selected_key_subscription" || core.Protocol != "codex-oauth-wham" && core.Protocol != "opencode-go-usage" {
		return noEvidence("account_authority_unverified")
	}
	groups := map[string]map[string]bool{}
	var presence struct {
		Windows []struct {
			UsedPercent       *float64 `json:"usedPercent"`
			RemainingFraction *float64 `json:"remainingFraction"`
		} `json:"windows"`
	}
	if json.Unmarshal([]byte(obs.WindowsJSON), &presence) != nil || len(presence.Windows) != len(core.Windows) {
		return noEvidence("invalid_evidence")
	}
	for _, w := range presence.Windows {
		if w.UsedPercent == nil || w.RemainingFraction == nil {
			return noEvidence("invalid_evidence")
		}
	}
	globalExhausted := false
	globalPositive := true
	modelExhausted := false
	for _, w := range core.Windows {
		if w.Name == "" || len(w.Name) > 128 || len(w.Model) > 128 || math.IsNaN(w.UsedPercent) || math.IsInf(w.UsedPercent, 0) || w.UsedPercent < 0 || w.UsedPercent > 100 || math.IsNaN(w.RemainingFraction) || math.IsInf(w.RemainingFraction, 0) || w.RemainingFraction < 0 || w.RemainingFraction > 1 {
			return noEvidence("invalid_evidence")
		}
		if w.SourceStatus != "allowed" && w.SourceStatus != "ok" && w.SourceStatus != "rate-limited" {
			return noEvidence("invalid_evidence")
		}
		reset, err := time.Parse(time.RFC3339Nano, w.ResetAt)
		if err != nil || !reset.After(now) {
			return noEvidence("reset_requires_recheck")
		}
		if groups[w.Model] == nil {
			groups[w.Model] = map[string]bool{}
		}
		if groups[w.Model][w.Name] {
			return noEvidence("invalid_evidence")
		}
		groups[w.Model][w.Name] = true
		exhausted := w.RemainingFraction == 0 && (w.UsedPercent == 100 || w.SourceStatus == "rate-limited")
		if w.SourceStatus == "rate-limited" && w.RemainingFraction != 0 || w.UsedPercent == 100 && w.RemainingFraction != 0 {
			return noEvidence("invalid_evidence")
		}
		globalExhausted = globalExhausted || w.Model == "" && exhausted
		modelExhausted = modelExhausted || w.Model != "" && exhausted
		if w.Model == "" {
			globalPositive = globalPositive && w.RemainingFraction > 0 && w.UsedPercent < 100 && w.SourceStatus != "rate-limited"
		}
	}
	// Model subgroups cannot widen a pause OR veto recovery of a distinct
	// whole/global reason. Only the complete global scope can vote here.
	if names := groups[""]; len(names) != 0 {
		if core.Protocol == "codex-oauth-wham" {
			if len(names) != 2 || !names["primary"] || !names["secondary"] {
				return noEvidence("incomplete_evidence")
			}
		} else {
			if len(names) != 3 || !names["rolling"] || !names["weekly"] || !names["monthly"] {
				return noEvidence("incomplete_evidence")
			}
		}
	}
	out := EvidenceDecision{Action: "none", AttemptID: obs.QueryID, ObservedAtMS: obs.ObservedAtMS, EvidenceVersion: status.SourceVersion + ":" + strconv.FormatInt(status.Version, 10) + ":" + obs.QueryID}
	if modelExhausted {
		out.ModelAutomationReason = "model_scope_unsupported"
	}
	if globalExhausted {
		out.Action = "pause"
		out.Reason = "quota_windows_exhausted"
		return out
	}
	if len(groups[""]) == 0 {
		if modelExhausted {
			return noEvidence("model_scope_unsupported")
		}
		return noEvidence("whole_credential_scope_unproven")
	}
	if globalPositive {
		out.Action = "recover"
		out.Reason = "quota_windows_positive"
		return out
	}
	return noEvidence("no_whole_credential_exhaustion")
}
