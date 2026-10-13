package worker

import (
	"context"
	"log"

	collectorpkg "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	automationsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/automation"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

type quotaAutomationWorker interface {
	Start(ctx context.Context)
	SetEnabled(enabled bool)
	HandleUsageEvents(ctx context.Context, cfg collectorpkg.RuntimeConfig, events []usage.Event)
	UpdateRuntimeConfig(ctx context.Context, cfg collectorpkg.RuntimeConfig)
}

type accountAutomationWorker interface {
	Start(ctx context.Context)
	SetAutoDisable(enabled bool)
	HandleUsageEvents(ctx context.Context, cfg collectorpkg.RuntimeConfig, events []usage.Event)
}

type codexResetAutomationWorker interface {
	Start(context.Context)
	SetEnabled(bool)
	UpdateRuntimeConfig(context.Context, collectorpkg.RuntimeConfig)
}

type AutomationRuntime struct {
	settings         *automationsvc.Service
	manager          *collectorpkg.Manager
	quotaWorker      quotaAutomationWorker
	accountWorker    accountAutomationWorker
	codexResetWorker codexResetAutomationWorker
	handler          *automationUsageHandler
}

func NewAutomationRuntime(settings *automationsvc.Service, manager *collectorpkg.Manager, quotaWorker quotaAutomationWorker, accountWorker accountAutomationWorker, resetWorkers ...codexResetAutomationWorker) *AutomationRuntime {
	var resetWorker codexResetAutomationWorker
	if len(resetWorkers) > 0 {
		resetWorker = resetWorkers[0]
	}
	handler := &automationUsageHandler{
		settings:         settings,
		quotaWorker:      quotaWorker,
		accountWorker:    accountWorker,
		codexResetWorker: resetWorker,
	}
	return &AutomationRuntime{
		settings:         settings,
		manager:          manager,
		quotaWorker:      quotaWorker,
		accountWorker:    accountWorker,
		codexResetWorker: resetWorker,
		handler:          handler,
	}
}

func (r *AutomationRuntime) Start(ctx context.Context) {
	if r == nil {
		return
	}
	// Apply the persisted policy before any worker starts so a paused policy
	// cannot take (or recover from) an automatic action during startup.
	r.applySettings(ctx)
	if r.quotaWorker != nil {
		r.quotaWorker.Start(ctx)
	}
	if r.accountWorker != nil {
		r.accountWorker.Start(ctx)
	}
	if r.codexResetWorker != nil {
		r.codexResetWorker.Start(ctx)
	}
	if r.manager != nil && r.handler != nil {
		r.manager.SetUsageEventHandler(r.handler)
	}
	r.logState(ctx, "loaded")
}

func (r *AutomationRuntime) UsageEventHandler() collectorpkg.UsageEventHandler {
	if r == nil {
		return nil
	}
	return r.handler
}

func (r *AutomationRuntime) Reload(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.applySettings(ctx)
	r.logState(ctx, "reloaded")
	return nil
}

// applySettings pushes the persisted account-processing policy into the workers
// that gate automatic actions. It is used both on startup (before the workers
// begin) and on every policy PATCH so OFF takes effect immediately.
func (r *AutomationRuntime) applySettings(ctx context.Context) {
	if r == nil || r.settings == nil {
		return
	}
	settings := r.settings.RuntimeSettings(ctx)
	if r.quotaWorker != nil {
		r.quotaWorker.SetEnabled(settings.QuotaCooldownEnabled)
	}
	if r.accountWorker != nil {
		r.accountWorker.SetAutoDisable(settings.AccountActionsAutoDisable)
	}
	if r.codexResetWorker != nil {
		r.codexResetWorker.SetEnabled(settings.QuotaCooldownEnabled)
	}
}

func (r *AutomationRuntime) logState(ctx context.Context, action string) {
	if r == nil || r.settings == nil {
		return
	}
	settings := r.settings.RuntimeSettings(ctx)
	log.Printf("[automation] runtime settings %s quotaCooldown=%t accountActions=%t accountActionsAutoDisable=%t", action, settings.QuotaCooldownEnabled, settings.AccountActionsEnabled, settings.AccountActionsAutoDisable)
}

type automationUsageHandler struct {
	settings         *automationsvc.Service
	quotaWorker      quotaAutomationWorker
	accountWorker    accountAutomationWorker
	codexResetWorker codexResetAutomationWorker
}

func (h *automationUsageHandler) HandleUsageEvents(ctx context.Context, cfg collectorpkg.RuntimeConfig, events []usage.Event) {
	if h == nil || len(events) == 0 || h.settings == nil {
		return
	}
	settings := h.settings.RuntimeSettings(ctx)
	if settings.QuotaCooldownEnabled && h.quotaWorker != nil {
		h.quotaWorker.HandleUsageEvents(ctx, cfg, events)
	}
	if settings.AccountActionsEnabled && h.accountWorker != nil {
		h.accountWorker.SetAutoDisable(settings.AccountActionsAutoDisable)
		h.accountWorker.HandleUsageEvents(ctx, cfg, events)
	}
}

func (h *automationUsageHandler) UpdateRuntimeConfig(ctx context.Context, cfg collectorpkg.RuntimeConfig) {
	if h == nil {
		return
	}
	if h.quotaWorker != nil {
		h.quotaWorker.UpdateRuntimeConfig(ctx, cfg)
	}
	if h.codexResetWorker != nil {
		h.codexResetWorker.UpdateRuntimeConfig(ctx, cfg)
	}
}
