package credentialpolicy_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	policy "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

func windowEvidence(t *testing.T) (cq.Status, time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	source := cq.Source{Binding: cq.Binding{Scope: rc.Scope{InstanceID: "00000000-0000-4000-8000-000000000001", InstanceIncarnation: "00000000-0000-4000-8000-000000000002", BootEpoch: "00000000-0000-4000-8000-000000000003", Revision: 2}, Targets: []rc.Target{{ResourceID: "00000000-0000-4000-8000-000000000004", Incarnation: "00000000-0000-4000-8000-000000000005", IdentityVersion: 1, CredentialVersion: 1, ConfigVersion: 1}}}, SourceVersion: strings.Repeat("a", 64), AuthIndex: "current-runtime", Protocol: "codex-oauth-wham", State: "ready", AccountProvenance: "selected_oauth_account"}
	core := cq.CoreObservation{Binding: source.Binding, SourceVersion: source.SourceVersion, ProtocolVersion: 1, Protocol: source.Protocol, QueryStatus: "success", AccountProvenance: source.AccountProvenance, TimestampProvenance: "upstream_http_date", ReceivedAtMS: now.UnixMilli(), ObservedAtMS: now.Add(-time.Second).UnixMilli(), Complete: true, Windows: []cq.Window{{Name: "primary", UsedPercent: 100, RemainingFraction: 0, ResetAt: now.Add(time.Hour).Format(time.RFC3339), SourceStatus: "allowed"}, {Name: "secondary", UsedPercent: 20, RemainingFraction: .8, ResetAt: now.Add(time.Hour).Format(time.RFC3339), SourceStatus: "allowed"}}}
	raw, _ := json.Marshal(core)
	binding, _ := json.Marshal(source.Binding)
	obs := &balance.Observation{QueryID: "00000000-0000-4000-8000-000000000006", Binding: binding, QueryStatus: "success", ObservedAtMS: core.ObservedAtMS, Semantic: "quota_windows", WindowsJSON: string(raw)}
	return cq.Status{Source: source, CredentialQuery: rc.Capability{State: "available"}, Version: 3, Attempt: obs, LastSuccess: obs}, now
}

// Positive evidence classification is NOT a positive ON/ACK/admission proof.
func TestCredentialEvidenceWholeScopeExhaustionIsOnlyAPauseCandidate(t *testing.T) {
	status, now := windowEvidence(t)
	decision := policy.ValidateCredentialEvidence(status, now)
	if decision.Action != "pause" || decision.Model != "" || decision.Reason != "quota_windows_exhausted" || decision.EvidenceVersion == "" {
		t.Fatalf("unexpected candidate %+v", decision)
	}
}

func TestCredentialEvidenceRecoveryRecomputesEveryScopedWindow(t *testing.T) {
	status, now := windowEvidence(t)
	var core cq.CoreObservation
	json.Unmarshal([]byte(status.Attempt.WindowsJSON), &core)
	for i := range core.Windows {
		core.Windows[i].UsedPercent = 20
		core.Windows[i].RemainingFraction = .8
	}
	core.RecoveryEligible = false // the aggregate is not the policy's route/scope proof
	raw, _ := json.Marshal(core)
	status.Attempt.WindowsJSON = string(raw)
	decision := policy.ValidateCredentialEvidence(status, now)
	if decision.Action != "recover" || decision.Reason != "quota_windows_positive" {
		t.Fatalf("fresh scoped positive not recognized: %+v", decision)
	}
}

func TestCredentialEvidenceNullNumbersCannotBecomeZeroExhaustion(t *testing.T) {
	status, now := windowEvidence(t)
	status.Attempt.WindowsJSON = strings.Replace(status.Attempt.WindowsJSON, `"remainingFraction":0`, `"remainingFraction":null`, 1)
	if d := policy.ValidateCredentialEvidence(status, now); d.Action != "none" {
		t.Fatalf("null became authoritative zero: %+v", d)
	}
}

func TestCredentialEvidenceRejectsFailuresStalenessResetAndModelOnlyProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*cq.Status, *cq.CoreObservation, time.Time)
	}{
		{"ordinary-429", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { s.Attempt.QueryStatus = "rate_limited" }},
		{"capacity-network", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { s.Attempt.QueryStatus = "network_error" }},
		{"auth-error", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { s.Attempt.QueryStatus = "auth_error" }},
		{"stale-last-success", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { s.Stale = true }},
		{"missing-data", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { s.LastSuccess = nil }},
		{"wrong-attempt", func(s *cq.Status, c *cq.CoreObservation, n time.Time) {
			copy := *s.LastSuccess
			copy.QueryID = "00000000-0000-4000-8000-000000000099"
			s.LastSuccess = &copy
		}},
		{"partial-with-recovery-bool", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { c.Complete = false; c.RecoveryEligible = true }},
		{"reset-expired", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { c.Windows[0].ResetAt = n.Format(time.RFC3339) }},
		{"old-observation", func(s *cq.Status, c *cq.CoreObservation, n time.Time) {
			c.ObservedAtMS = n.Add(-time.Hour).UnixMilli()
			s.Attempt.ObservedAtMS = c.ObservedAtMS
		}},
		{"source-swap", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { c.SourceVersion = strings.Repeat("b", 64) }},
		{"instance-swap", func(s *cq.Status, c *cq.CoreObservation, n time.Time) {
			c.Scope.InstanceIncarnation = "00000000-0000-4000-8000-000000000099"
		}},
		{"unproven-account", func(s *cq.Status, c *cq.CoreObservation, n time.Time) {
			s.AccountProvenance = "unverified"
			c.AccountProvenance = "unverified"
		}},
		{"missing-window", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { c.Windows = c.Windows[:1] }},
		{"duplicate-window", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { c.Windows[1] = c.Windows[0] }},
		{"model-only-exhaustion", func(s *cq.Status, c *cq.CoreObservation, n time.Time) {
			for i := range c.Windows {
				c.Windows[i].Model = "specific-model"
			}
			c.RecoveryEligible = true
		}},
		{"generic429-core", func(s *cq.Status, c *cq.CoreObservation, n time.Time) { c.QueryStatus = "rate_limited" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, now := windowEvidence(t)
			var core cq.CoreObservation
			json.Unmarshal([]byte(status.Attempt.WindowsJSON), &core)
			tc.change(&status, &core, now)
			raw, _ := json.Marshal(core)
			status.Attempt.WindowsJSON = string(raw)
			if decision := policy.ValidateCredentialEvidence(status, now); decision.Action != "none" {
				t.Fatalf("invalid evidence caused action %+v", decision)
			}
		})
	}
}

func TestCredentialEvidenceAcceptsConfiguredQwenCLIProvenance(t *testing.T) {
	status, now := windowEvidence(t)
	status.Protocol = "qwen-coding-plan-cli"
	status.AccountProvenance = "configured_cli_profile"
	var core cq.CoreObservation
	if err := json.Unmarshal([]byte(status.Attempt.WindowsJSON), &core); err != nil {
		t.Fatal(err)
	}
	core.Protocol = status.Protocol
	core.AccountProvenance = status.AccountProvenance
	core.TimestampProvenance = "local_cli_acquisition_time"
	core.SourceVersion = status.SourceVersion
	core.Binding = status.Binding
	core.RecoveryEligible = true
	for i := range core.Windows {
		core.Windows[i].Name = []string{"rolling", "weekly"}[i]
		core.Windows[i].UsedPercent = 20
		core.Windows[i].RemainingFraction = .8
	}
	core.Windows = append(core.Windows, cq.Window{Name: "monthly", UsedPercent: 20, RemainingFraction: .8, ResetAt: now.Add(time.Hour).Format(time.RFC3339), SourceStatus: "allowed"})
	raw, _ := json.Marshal(core)
	status.Attempt.WindowsJSON = string(raw)
	status.LastSuccess = status.Attempt
	decision := policy.ValidateCredentialEvidence(status, now)
	if decision.Action != "recover" || decision.Reason != "quota_windows_positive" {
		t.Fatalf("configured Qwen CLI evidence was rejected: %+v", decision)
	}
}
