package credentialquota

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
)

func codexResetCreditFixture(t *testing.T, reset *ResetCredits) (CoreObservation, Source) {
	t.Helper()
	now := time.Now()
	source := Source{
		Binding: Binding{
			Scope:   rc.Scope{InstanceID: "instance", InstanceIncarnation: "incarnation", BootEpoch: "boot"},
			Targets: []rc.Target{{ResourceID: "resource", Incarnation: "target", IdentityVersion: 1, CredentialVersion: 1, ConfigVersion: 1}},
		},
		SourceVersion:     strings.Repeat("a", 64),
		SourceID:          "codex-oauth",
		SubjectID:         "subject",
		AuthIndex:         "auth-index",
		Protocol:          "codex-oauth-wham",
		State:             "ready",
		AccountProvenance: "selected_oauth_account",
	}
	resetAt := now.Add(time.Hour).UTC().Format(time.RFC3339)
	observation := CoreObservation{
		Binding:             source.Binding,
		SourceVersion:       source.SourceVersion,
		ProtocolVersion:     1,
		TimestampProvenance: "upstream_http_date",
		ReceivedAtMS:        now.UnixMilli(),
		Protocol:            source.Protocol,
		QueryStatus:         "success",
		ObservedAtMS:        now.UnixMilli(),
		Windows: []Window{
			{Name: "primary", UsedPercent: 25, RemainingFraction: 0.75, ResetAt: resetAt, SourceStatus: "allowed", LimitWindowSeconds: 18000},
			{Name: "secondary", UsedPercent: 50, RemainingFraction: 0.5, ResetAt: resetAt, SourceStatus: "allowed", LimitWindowSeconds: 604800},
		},
		ResetCredits:      reset,
		Complete:          true,
		AccountProvenance: source.AccountProvenance,
		RecoveryEligible:  true,
	}
	return observation, source
}

func TestValidAcceptsCodexResetCredits(t *testing.T) {
	applicable := 0
	observation, source := codexResetCreditFixture(t, &ResetCredits{AvailableCount: 1, ApplicableAvailableCount: &applicable})
	if !valid(observation, source) {
		t.Fatal("codex reset credits were rejected")
	}
	// The allowance rides on the same persisted observation the panel reads.
	doc, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), `"resetCredits":{"availableCount":1,"applicableAvailableCount":0}`) {
		t.Fatalf("reset credits missing from the persisted observation: %s", doc)
	}
	observation.ResetCredits = &ResetCredits{AvailableCount: 0}
	if !valid(observation, source) {
		t.Fatal("consumed reset credit count was rejected")
	}
}

func TestValidRejectsUnsupportedResetCreditEvidence(t *testing.T) {
	tooMany := 1001
	negative := -1
	overApplicable := 2
	negativeApplicable := -1
	for _, tc := range []struct {
		name      string
		protocol  string
		reset     *ResetCredits
		wantValid bool
	}{
		{name: "count above bound", reset: &ResetCredits{AvailableCount: tooMany}},
		{name: "negative count", reset: &ResetCredits{AvailableCount: negative}},
		{name: "applicable above granted", reset: &ResetCredits{AvailableCount: 1, ApplicableAvailableCount: &overApplicable}},
		{name: "negative applicable", reset: &ResetCredits{AvailableCount: 1, ApplicableAvailableCount: &negativeApplicable}},
		{name: "non-codex protocol", protocol: "opencode-go-usage", reset: &ResetCredits{AvailableCount: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation, source := codexResetCreditFixture(t, tc.reset)
			if tc.protocol != "" {
				observation.Protocol = tc.protocol
				source.Protocol = tc.protocol
			}
			if valid(observation, source) {
				t.Fatal("unsupported reset credit evidence was published")
			}
		})
	}
}
