package automation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type stubSettingsStore struct {
	settings store.AutomationSettings
	ok       bool
	err      error
	saves    int
}

func (s *stubSettingsStore) LoadAutomationSettings(ctx context.Context) (store.AutomationSettings, bool, error) {
	return s.settings, s.ok, s.err
}

func (s *stubSettingsStore) SaveAutomationSettings(ctx context.Context, settings store.AutomationSettings) (store.AutomationSettings, error) {
	s.saves++
	settings.UpdatedAtMS = 1
	s.settings = settings
	s.ok = true
	return settings, nil
}

func mustStatus(t *testing.T, svc *Service, ctx context.Context) Status {
	t.Helper()
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatalf("unexpected Status error: %v", err)
	}
	return status
}

func TestStatusExposesEffectiveFlagsAndKeys(t *testing.T) {
	status := mustStatus(t, New(config.Config{
		AccountActionsEnabled:     true,
		AccountActionsAutoDisable: false,
	}), context.Background())

	if status.Source != SourceStartup {
		t.Fatalf("source = %q, want %q", status.Source, SourceStartup)
	}
	if !status.AccountActions.Enabled || status.AccountActions.EnvKey != "USAGE_ACCOUNT_ACTIONS_ENABLED" || status.AccountActions.ConfigFileKey != "accountActionsEnabled" {
		t.Fatalf("accountActions = %#v", status.AccountActions)
	}
	if status.AccountActions.DependsOn != "" {
		t.Fatalf("accountActions should not declare a dependency, got %q", status.AccountActions.DependsOn)
	}
	if status.AccountActionsAutoDisable.Enabled {
		t.Fatalf("accountActionsAutoDisable should be disabled, got %#v", status.AccountActionsAutoDisable)
	}
	if status.AccountActionsAutoDisable.EnvKey != "USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE" || status.AccountActionsAutoDisable.ConfigFileKey != "accountActionsAutoDisable" {
		t.Fatalf("accountActionsAutoDisable keys = %#v", status.AccountActionsAutoDisable)
	}
	if status.AccountActionsAutoDisable.DependsOn != "authIssueQueue" {
		t.Fatalf("accountActionsAutoDisable dependsOn = %q", status.AccountActionsAutoDisable.DependsOn)
	}
}

func TestStatusAutoDisableReportsEffectiveValue(t *testing.T) {
	status := mustStatus(t, New(config.Config{
		AccountActionsEnabled:     false,
		AccountActionsAutoDisable: true,
	}), context.Background())
	if status.AccountActions.Enabled {
		t.Fatalf("accountActions should be disabled, got %#v", status.AccountActions)
	}
	if status.AccountActionsAutoDisable.Enabled {
		t.Fatalf("accountActionsAutoDisable should not be effective when accountActions is disabled, got %#v", status.AccountActionsAutoDisable)
	}
	if status.AccountActionsAutoDisable.DependsOn != "authIssueQueue" {
		t.Fatalf("accountActionsAutoDisable dependsOn = %q", status.AccountActionsAutoDisable.DependsOn)
	}

	status = mustStatus(t, New(config.Config{
		AccountActionsEnabled:     true,
		AccountActionsAutoDisable: true,
	}), context.Background())
	if !status.AccountActionsAutoDisable.Enabled {
		t.Fatalf("accountActionsAutoDisable should be effective when accountActions is enabled, got %#v", status.AccountActionsAutoDisable)
	}
}

func TestStatusUsesDBSettingsUnlessEnvLocked(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/usage.sqlite")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if _, err := st.SaveAutomationSettings(ctx, store.AutomationSettings{
		AccountActionsEnabled:     boolPtr(false),
		AccountActionsAutoDisable: boolPtr(true),
	}); err != nil {
		t.Fatalf("save automation settings: %v", err)
	}
	status := mustStatus(t, New(config.Config{}, st), ctx)
	if status.AccountActions.Enabled || status.AccountActions.Source != SourceDB {
		t.Fatalf("accountActions = %#v", status.AccountActions)
	}
	if status.AccountActionsAutoDisable.Enabled || !status.AccountActionsAutoDisable.Configured || status.AccountActionsAutoDisable.Source != SourceDB {
		t.Fatalf("accountActionsAutoDisable = %#v", status.AccountActionsAutoDisable)
	}
}

func TestUpdateRejectsEnvLockedFields(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/usage.sqlite")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	_, err = New(config.Config{AccountActionsEnvSet: true}, st).Update(context.Background(), UpdateRequest{AccountActionsEnabled: boolPtr(true)})
	if err == nil || !strings.Contains(err.Error(), "locked by environment variable") {
		t.Fatalf("Update err = %v", err)
	}
}

func TestStatusDefaultsAllOff(t *testing.T) {
	status := mustStatus(t, New(config.Config{}), context.Background())
	if status.AccountActions.Enabled || status.AccountActionsAutoDisable.Enabled {
		t.Fatalf("expected all account actions disabled by default, got %#v", status)
	}
}

func TestStatusReturnsErrorWhenLoadFails(t *testing.T) {
	svc := &Service{
		cfg:   config.Config{},
		store: &stubSettingsStore{err: errors.New("simulated db read failure")},
	}
	if _, err := svc.Status(context.Background()); err == nil {
		t.Fatalf("expected Status to surface load error, got nil")
	}
}

func TestRuntimeSettingsKeepsLastKnownOnLoadError(t *testing.T) {
	ctx := context.Background()
	loader := &stubSettingsStore{
		settings: store.AutomationSettings{AccountActionsEnabled: boolPtr(true)},
		ok:       true,
	}
	svc := &Service{cfg: config.Config{}, store: loader}

	first := svc.RuntimeSettings(ctx)
	if !first.AccountActionsEnabled {
		t.Fatalf("first runtime load should reflect DB value, got %#v", first)
	}

	loader.err = errors.New("simulated db read failure")
	second := svc.RuntimeSettings(ctx)
	if !second.AccountActionsEnabled {
		t.Fatalf("runtime gating should keep last known config on read failure, got %#v", second)
	}

	fresh := &Service{
		cfg:   config.Config{},
		store: &stubSettingsStore{err: errors.New("simulated db read failure")},
	}
	fallback := fresh.RuntimeSettings(ctx)
	if fallback.AccountActionsEnabled {
		t.Fatalf("runtime gating should fall back to startup default before first successful load, got %#v", fallback)
	}
}

// TestUpdateSerializesConcurrentPatches is a regression guard for the
// read-modify-write race: many concurrent PATCH requests targeting different
// fields must all be persisted.
func TestUpdateSerializesConcurrentPatches(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/usage.sqlite")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	svc := New(config.Config{}, st)

	patches := []UpdateRequest{
		{AccountActionsEnabled: boolPtr(true)},
		{AccountActionsAutoDisable: boolPtr(true)},
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		patch := patches[i%len(patches)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Update(ctx, patch); err != nil {
				t.Errorf("concurrent Update failed: %v", err)
			}
		}()
	}
	wg.Wait()

	status := mustStatus(t, svc, ctx)
	if !status.AccountActions.Enabled {
		t.Fatalf("accountActions field lost under concurrent updates: %#v", status)
	}
	if !status.AccountActionsAutoDisable.Configured {
		t.Fatalf("accountActionsAutoDisable field lost under concurrent updates: %#v", status)
	}
	if status.AccountActionsAutoDisable.Enabled != (status.AccountActions.Enabled && status.AccountActionsAutoDisable.Configured) {
		t.Fatalf("auto-disable effective value inconsistent: %#v", status)
	}
}

// TestUpdateReturnsPersistedRecordWithoutRereading verifies that Update builds
// its response from the record the repository just persisted (with an
// UpdatedAtMS), not from a re-read.
func TestUpdateReturnsPersistedRecordWithoutRereading(t *testing.T) {
	loader := &stubSettingsStore{ok: false}
	svc := &Service{cfg: config.Config{}, store: loader}

	status, err := svc.Update(context.Background(), UpdateRequest{AccountActionsEnabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if !status.AccountActions.Enabled {
		t.Fatalf("response should expose the persisted account action policy, got %#v", status.AccountActions)
	}
	if status.UpdatedAtMS == 0 {
		t.Fatalf("response should carry the UpdatedAtMS assigned by the store, got 0")
	}
	runtime := svc.RuntimeSettings(context.Background())
	if !runtime.AccountActionsEnabled {
		t.Fatalf("runtime cache should reflect persisted value after Update, got %#v", runtime)
	}
}
