package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

// Opt-in loopback host for the real UI/API/temporary SQLite seam. All Core,
// BrowserSkill and page dependencies are synthetic; no real browser is invoked.
func TestOfficialBalanceWebUIFixture(t *testing.T) {
	dir := os.Getenv("WEB_BALANCE_UI_FIXTURE")
	if dir == "" {
		t.Skip("opt-in synthetic web UI host")
	}
	if _, err := os.Stat(filepath.Join(dir, "fixture.json")); !os.IsNotExist(err) {
		t.Fatal("fresh fixture directory required")
	}
	x := newWebObservationFixture(t)
	x.verified.Store(false)
	var delay atomic.Int64
	x.readHook.Store(func(ctx context.Context, _ int64) error {
		timer := time.NewTimer(time.Duration(delay.Load()) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	control := officialBrowserServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testutil.AdminKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var state struct {
			Enroll  *bool   `json:"enroll"`
			Stage   *string `json:"stage"`
			Amount  *string `json:"amount"`
			Missing bool    `json:"missing"`
			DelayMS int64   `json:"delayMs"`
		}
		if r.Method != "PUT" || json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&state) != nil || state.DelayMS < 0 || state.DelayMS > 2000 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if state.Enroll != nil {
			// Only the controlled dependency gains authority. This is not a
			// production enrollment endpoint or an operator assertion accepted as proof.
			var status map[string]any
			if json.Unmarshal([]byte(x.f.status()), &status) != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			authority, _ := status["webSourceAuthority"].(string)
			x.authority.Store(authority)
			x.verified.Store(*state.Enroll)
		}
		if state.Stage != nil {
			x.stage.Store(*state.Stage)
		}
		page := x.page.Load().(balance.WebPage)
		if state.Missing {
			page.Amount = nil
		} else if state.Amount != nil {
			page.Amount = state.Amount
		}
		x.page.Store(page)
		delay.Store(state.DelayMS)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(control.Close)
	manager := officialBrowserServer(t, x.f.handler)
	t.Cleanup(manager.Close)
	config := webConfiguration(0)
	config["web"].(map[string]any)["parsingRule"] = syntheticWebRule
	data, err := json.Marshal(map[string]any{
		"synthetic": true, "base": manager.URL, "control": control.URL,
		"managementKey": testutil.AdminKey, "config": config,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("FIXTURE_READY:", dir)
	for i := 0; i < 1200; i++ {
		if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("synthetic UI fixture was not stopped within 120 seconds")
}
