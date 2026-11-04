package balance_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

// Opt-in live BrowserSkill path: CPA_LIVE_LAYSO=1 plus a connected Edge instance
// already logged into layso.ai Profile 1. Does not push, deploy, or close issues.
func TestLiveLaysoEnrolledDashboardModelsQuery(t *testing.T) {
	if os.Getenv("CPA_LIVE_LAYSO") == "" {
		t.Skip("set CPA_LIVE_LAYSO=1 with logged-in Profile 1 BrowserSkill")
	}
	instance := os.Getenv("CPA_LIVE_LAYSO_INSTANCE")
	if instance == "" {
		instance = "5cfdda27"
	}
	execUser := os.Getenv("CPA_LIVE_LAYSO_EXECUTION_USER")
	if execUser == "" {
		execUser = "will"
	}
	src := balance.WebConfig{
		Site: "layso", AccountID: "h7w8", TenantID: "Personal", Role: "Personal",
		ExecutionUser: execUser,
		Browser:       balance.WebBrowserIdentity{Family: "microsoft-edge", Profile: "Profile 1", InstanceID: instance},
		Currency:      "USD", ParsingRule: balance.LaysoDashboardModelsRuleV1,
	}
	req := balance.WebRequest{
		Authority: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Source:    src,
	}
	reader := balance.NewBrowserSkill(balance.BrowserSkillOptions{
		Rules: map[string]func([]byte) (balance.WebPage, error){
			balance.LaysoDashboardModelsRuleV1: balance.ParseLaysoDashboardModelsHTML,
		},
		Enrollment: func(q balance.WebRequest) (balance.WebEnrollment, bool) {
			if q.Authority != req.Authority || q.Source != src {
				return balance.WebEnrollment{}, false
			}
			return balance.WebEnrollment{
				Authority: q.Authority, Source: q.Source,
				ProfileVerified: true, ExecutionUserVerified: true, ParserVerified: true,
				ProofReference: "live-host-profile1-" + instance,
				EvidenceKind:   "verified_browser",
			}, true
		},
	})
	if !reader.Ready(req) {
		t.Fatal("enrolled reader not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out := reader.Query(ctx, req)
	if out.QueryStatus != "success" || out.AmountDecimal == "" || out.Currency != "USD" {
		t.Fatalf("live enrolled query failed: %+v", out)
	}
	t.Logf("live layso balance amount=%s currency=%s evidence=%+v", out.AmountDecimal, out.Currency, out.WebEvidence)
}
