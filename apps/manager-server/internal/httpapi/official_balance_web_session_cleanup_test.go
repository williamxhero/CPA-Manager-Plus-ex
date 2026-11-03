package httpapi

import (
	"reflect"
	"testing"
)

func TestOfficialBalanceWebSessionBudgetCleansOwnSession(t *testing.T) {
	assertWebSessionBudgetFailure(t, "budget-session-valid", true, "WEB_SESSION_UNAVAILABLE")
}

func TestOfficialBalanceWebSessionBudgetRejectsAmbiguousID(t *testing.T) {
	assertWebSessionBudgetFailure(t, "budget-session-duplicate", false, "WEB_SESSION_UNAVAILABLE")
}

func TestOfficialBalanceWebSessionBudgetCleanupBoundaries(t *testing.T) {
	for _, stage := range []string{"partial-start", "malformed-browser", "wrong-browser", "revoked"} {
		t.Run(stage, func(t *testing.T) {
			assertWebSessionBudgetFailure(t, "budget-session-"+stage, true, "WEB_SESSION_UNAVAILABLE")
		})
	}
	for _, stage := range []string{"stop-failed", "stop-output"} {
		t.Run(stage, func(t *testing.T) {
			assertWebSessionBudgetFailure(t, "budget-session-"+stage, true, "WEB_CLEANUP_FAILED")
		})
	}
}

func TestOfficialBalanceWebSessionBudgetNeverGuessesOwnership(t *testing.T) {
	for _, stage := range []string{"escaped-duplicate", "missing", "nested", "null", "number", "unsafe", "long", "malformed", "trailing", "oversized"} {
		t.Run(stage, func(t *testing.T) {
			assertWebSessionBudgetFailure(t, "budget-session-"+stage, false, "WEB_SESSION_UNAVAILABLE")
		})
	}
}

// The public management API and real temporary SQLite remain the assertions;
// command calls are the controlled external browser boundary, never a live CLI.
func assertWebSessionBudgetFailure(t *testing.T, stage string, ownID bool, errorCode string) {
	t.Helper()
	x := newWebObservationFixture(t)
	x.configure(t)
	last := x.result(t)["lastSuccess"]
	if last == nil {
		t.Fatal("missing baseline persisted capture")
	}
	x.mu.Lock()
	before := len(x.calls)
	x.mu.Unlock()
	x.stage.Store(stage)
	status := x.result(t)
	attempt := status["attempt"].(map[string]any)
	if attempt["queryStatus"] == "success" || attempt["errorCode"] != errorCode || attempt["amountDecimal"] != nil || attempt["observedAtMs"] != nil || attempt["webEvidence"] != nil || status["stale"] != true || !reflect.DeepEqual(last, status["lastSuccess"]) {
		t.Fatal("over-budget session response renewed or lost success", status)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	starts, stops := 0, 0
	for _, args := range x.calls[before:] {
		if args[0] == "navigate" || args[0] == "observe" {
			t.Fatal("over-budget session reached the page", args)
		}
		if len(args) > 1 && args[0] == "session" {
			if args[1] == "start" {
				starts++
			}
			if args[1] == "stop" {
				stops++
				if !ownID || !reflect.DeepEqual(args, []string{"session", "stop", "synthetic-owned-session"}) {
					t.Fatal("stopped an unverified or foreign session", args)
				}
			}
		}
	}
	expectedStops := 0
	if ownID {
		expectedStops = 1
	}
	if starts != 1 || stops != expectedStops {
		t.Fatalf("own session cleanup mismatch: starts=%d stops=%d expectedStops=%d", starts, stops, expectedStops)
	}
}
