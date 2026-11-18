package credentialquota

import "testing"

func TestAntigravityQuotaDisplayEvidence(t *testing.T) {
	observation, source := codexResetCreditFixture(t, nil)
	observation.Protocol = "antigravity-oauth-quota-summary"
	source.Protocol = observation.Protocol
	observation.AccountProvenance = "selected_oauth_project"
	source.AccountProvenance = observation.AccountProvenance
	observation.Complete = false
	observation.RecoveryEligible = false
	observation.Windows[0].Name = "rolling"
	observation.Windows[0].Model = "Gemini Models"
	observation.Windows[1].Name = "weekly"
	observation.Windows[1].Model = "Gemini Models"
	if !valid(observation, source) {
		t.Fatal("selected Antigravity quota summary was rejected")
	}
	observation.Complete = true
	observation.RecoveryEligible = true
	if valid(observation, source) {
		t.Fatal("display-only Antigravity summary acquired automatic inference recovery authority")
	}
}
