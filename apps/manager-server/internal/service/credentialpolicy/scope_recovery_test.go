package credentialpolicy_test

import (
	"encoding/json"
	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	"testing"
	"time"
)

func TestCredentialEvidenceCandidateExpiresAtEarliestGlobalReset(t *testing.T) {
	status, now := windowEvidence(t)
	var core cq.CoreObservation
	if err := json.Unmarshal([]byte(status.Attempt.WindowsJSON), &core); err != nil {
		t.Fatal(err)
	}
	core.Windows[0].ResetAt = now.Add(2 * time.Second).Format(time.RFC3339)
	model := core.Windows[0]
	model.Model = "unrelated-model"
	model.ResetAt = now.Add(time.Second).Format(time.RFC3339)
	core.Windows = append(core.Windows, model)
	raw, _ := json.Marshal(core)
	status.Attempt.WindowsJSON = string(raw)
	d := policy.ValidateCredentialEvidence(status, now)
	if d.Action != "pause" || d.ValidUntilMS != now.Add(2*time.Second).UnixMilli() {
		t.Fatalf("global evidence did not expire at own reset boundary: %+v", d)
	}
}

// Evidence candidate seam only; final own-clear/other-owner preservation still
// requires actual producer admission/readback, not a fake applied gate fixture.
func TestCredentialEvidenceGlobalRecoveryDoesNotRequireDepletedModelRecovery(t *testing.T) {
	for _, name := range []string{"global-positive-model-depleted", "missing-global-window", "model-only", "incomplete"} {
		t.Run(name, func(t *testing.T) {
			status, now := windowEvidence(t)
			if d := policy.ValidateCredentialEvidence(status, now); d.Action != "pause" || d.Model != "" {
				t.Fatal("fixture did not originate whole/global depletion")
			}
			var core cq.CoreObservation
			if err := json.Unmarshal([]byte(status.Attempt.WindowsJSON), &core); err != nil {
				t.Fatal(err)
			}
			for i := range core.Windows {
				core.Windows[i].RemainingFraction = .8
				core.Windows[i].UsedPercent = 20
			}
			model := core.Windows[0]
			model.Model = "unrelated-depleted-model"
			model.RemainingFraction = 0
			model.UsedPercent = 100
			core.Windows = append(core.Windows, model)
			switch name {
			case "missing-global-window":
				core.Windows = core.Windows[1:]
			case "model-only":
				core.Windows = core.Windows[2:]
			case "incomplete":
				core.Complete = false
			}
			raw, _ := json.Marshal(core)
			status.Attempt.WindowsJSON = string(raw)
			d := policy.ValidateCredentialEvidence(status, now)
			if name == "global-positive-model-depleted" {
				if d.Action != "recover" || d.Model != "" || d.Reason != "quota_windows_positive" || d.ModelAutomationReason != "model_scope_unsupported" {
					t.Fatalf("model depletion perpetuated global ban: %+v", d)
				}
			} else if d.Action != "none" {
				t.Fatalf("unproven global scope recovered: %+v", d)
			}
		})
	}
}
