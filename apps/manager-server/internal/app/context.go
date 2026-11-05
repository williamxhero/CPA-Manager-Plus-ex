package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/buildinfo"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	accountactionsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/accountaction"
	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
	apikeyaliassvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/apikeyalias"
	automationsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/automation"
	bootstrapsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrap"
	codexinspectionsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/codexinspection"
	collectorsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpaauthfiles"
	credentialpolicysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialpolicy"
	credentialquotasvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	dashboardsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/dashboard"
	fullrefreshsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managementquery"
	managerconfigsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	modelpricesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/modelprice"
	monitoringsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	officialbalancesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	panelsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/panel"
	providergatesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providergate"
	providerkeyaliassvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerkeyalias"
	providerpolicysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/providerpolicy"
	proxysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxy"
	quotasnapshotsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/quotasnapshot"
	resourcecapabilitysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	setupsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/setup"
	updatechecksvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/updatecheck"
	usagesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type AutomationRuntimeService interface {
	Reload(ctx context.Context) error
}

type QuotaCooldownRecoveryService interface {
	RecoverOwnedCooldown(ctx context.Context, authFileName string, authIndex string, provider string) (bool, error)
}

type DatabaseMaintenanceStatusProvider interface {
	Snapshot() sqliterepo.WALMaintenanceSnapshot
}

// FullRefreshCoordinator keeps host APIs replaceable by the same76 service in
// owner tests while allowing additive policy runtime lifecycle wiring.
type FullRefreshCoordinator interface {
	Inventory(context.Context) (fullrefreshsvc.Inventory, error)
	StartRound(context.Context, fullrefreshsvc.Purpose) (fullrefreshsvc.Progress, error)
	StartSingleSource(context.Context, fullrefreshsvc.SourceRequest, fullrefreshsvc.Purpose) (fullrefreshsvc.Progress, error)
	GetProgress(context.Context, string) (fullrefreshsvc.Progress, error)
	Cancel(context.Context, string) (fullrefreshsvc.Progress, error)
	StartOfficial(context.Context, officialbalancesvc.Query) (officialbalancesvc.Job, error)
	StartCredential(context.Context, credentialquotasvc.Query) (credentialquotasvc.Job, error)
	Subscribe(fullrefreshsvc.Observer)
	SubscribeStarted(fullrefreshsvc.StartedObserver)
	Run(context.Context) error
	Shutdown(context.Context) error
}

type Context struct {
	ProviderPolicyService   *providerpolicysvc.Service
	ProviderGateService     *providergatesvc.Service
	CredentialPolicyService *credentialpolicysvc.Service
	ManagementQueryBudget   *managementquery.Budget
	FullRefreshService      FullRefreshCoordinator
	OfficialBalanceService  *officialbalancesvc.Service
	CredentialQuotaService  *credentialquotasvc.Service
	UpdateCheckService      *updatechecksvc.Service
	Config                  config.Config
	Store                   *store.Store
	Collector               *collector.Manager

	StartedAt int64
	ServiceID string
	Bootstrap bootstrapsvc.Result

	SetupService                   *setupsvc.Service
	AdminAuthService               *adminauthsvc.Service
	ManagerConfigService           *managerconfigsvc.Service
	CollectorService               *collectorsvc.Service
	UsageService                   *usagesvc.Service
	DashboardService               *dashboardsvc.Service
	CodexInspectionService         *codexinspectionsvc.Service
	MonitoringService              *monitoringsvc.Service
	QuotaSnapshotService           *quotasnapshotsvc.Service
	ModelPriceService              *modelpricesvc.Service
	APIKeyAliasService             *apikeyaliassvc.Service
	ProviderKeyAliasService        *providerkeyaliassvc.Service
	AccountActionService           *accountactionsvc.Service
	AccountProcessingPolicyService *automationsvc.Service
	ResourceCapabilityService      *resourcecapabilitysvc.Service
	AuthFileMutationCoordinator    *cpaauthfiles.MutationCoordinator
	QuotaCooldownRecoveryService   QuotaCooldownRecoveryService
	ProxyService                   *proxysvc.Service
	PanelService                   *panelsvc.Service
	AutomationRuntimeService       AutomationRuntimeService
	DatabaseMaintenance            DatabaseMaintenanceStatusProvider
}

func FromExisting(
	cfg config.Config,
	st *store.Store,
	collectorManager *collector.Manager,
	startedAt int64,
	embeddedPanel fs.FS,
	modelPriceSyncURL *string,
	openRouterModelPriceSyncURL *string,
	serviceID string,
	automationRuntimeService ...AutomationRuntimeService,
) *Context {
	return fromExisting(
		cfg,
		st,
		collectorManager,
		startedAt,
		embeddedPanel,
		nil,
		modelPriceSyncURL,
		openRouterModelPriceSyncURL,
		serviceID,
		automationRuntimeService...,
	)
}

func FromExistingWithModelsDev(
	cfg config.Config,
	st *store.Store,
	collectorManager *collector.Manager,
	startedAt int64,
	embeddedPanel fs.FS,
	modelsDevModelPriceSyncURL *string,
	modelPriceSyncURL *string,
	openRouterModelPriceSyncURL *string,
	serviceID string,
	automationRuntimeService ...AutomationRuntimeService,
) *Context {
	return fromExisting(
		cfg,
		st,
		collectorManager,
		startedAt,
		embeddedPanel,
		modelsDevModelPriceSyncURL,
		modelPriceSyncURL,
		openRouterModelPriceSyncURL,
		serviceID,
		automationRuntimeService...,
	)
}

func fromExisting(
	cfg config.Config,
	st *store.Store,
	collectorManager *collector.Manager,
	startedAt int64,
	embeddedPanel fs.FS,
	modelsDevModelPriceSyncURL *string,
	modelPriceSyncURL *string,
	openRouterModelPriceSyncURL *string,
	serviceID string,
	automationRuntimeService ...AutomationRuntimeService,
) *Context {
	var runtimeService AutomationRuntimeService
	if len(automationRuntimeService) > 0 {
		runtimeService = automationRuntimeService[0]
	}
	collectorService := collectorsvc.New(collectorManager)
	managerConfigService := managerconfigsvc.New(cfg, st, collectorService)
	accountProcessingPolicyService := automationsvc.New(cfg, st)
	usageImportBaseDir := strings.TrimSpace(cfg.DataDir)
	if usageImportBaseDir == "" {
		usageImportBaseDir = filepath.Dir(cfg.DBPath)
	}
	usageArchiveDir := strings.TrimSpace(cfg.UsageArchiveDir)
	if usageArchiveDir == "" {
		usageArchiveDir = filepath.Join(usageImportBaseDir, "usage-archives")
	}
	usageService := usagesvc.New(st, usagesvc.WithImportSessions(usagesvc.ImportSessionConfig{
		Directory:      filepath.Join(usageImportBaseDir, "usage-imports"),
		ChunkSizeBytes: cfg.UsageImportChunkBytes,
		DiskQuotaBytes: cfg.UsageImportDiskQuotaBytes,
		MaxSessions:    cfg.UsageImportMaxSessions,
		TTL:            cfg.UsageImportSessionTTL,
	}), usagesvc.WithArchive(usagesvc.ArchiveConfig{
		Directory:             usageArchiveDir,
		AggregateReadsEnabled: cfg.DashboardHourlyRollupEnabled,
	}))
	authFileMutationCoordinator := cpaauthfiles.NewMutationCoordinator()
	identityService := resourcecapabilitysvc.New(accountProcessingPolicyService, managerConfigService)
	managementBudget := managementquery.NewBudget(st)
	officialBalanceService := officialbalancesvc.New(st, identityService, officialbalancesvc.WithBudget(managementBudget))
	if err := loadHostWebBrowserAttestation(context.Background(), officialBalanceService); err != nil {
		panic(fmt.Sprintf("host web browser attestation: %v", err))
	}
	webReader, refreshClock := managementRuntimeDependencies()
	if webReader != nil {
		officialBalanceService.WebReader = webReader
	}
	credentialQuotaService := credentialquotasvc.New(st, identityService, credentialquotasvc.WithBudget(managementBudget))
	providerGateService := providergatesvc.New(st, managerConfigService, identityService)
	out := &Context{
		ProviderPolicyService:   providerpolicysvc.New(st, identityService, managerConfigService, officialBalanceService),
		ProviderGateService:     providergatesvc.New(st, managerConfigService, identityService),
		CredentialPolicyService: credentialpolicysvc.New(st, credentialQuotaService, managerConfigService),
		ManagementQueryBudget:   managementBudget,
		FullRefreshService:      fullrefreshsvc.New(st, identityService, officialBalanceService, credentialQuotaService, fullrefreshsvc.WithBudget(managementBudget), fullrefreshsvc.WithClock(refreshClock)),
		OfficialBalanceService:  officialBalanceService,
		CredentialQuotaService:  credentialQuotaService,
		UpdateCheckService:      updatechecksvc.New(st, buildinfo.Version, buildinfo.SourceCommit, os.Getenv("CPAMP_UPDATE_CHECK_ENABLED") != "false"),
		Config:                  cfg,
		Store:                   st,
		Collector:               collectorManager,
		StartedAt:               startedAt,
		ServiceID:               serviceID,
		AdminAuthService:        adminauthsvc.New(cfg, st),
		SetupService:            setupsvc.New(cfg, st, collectorService, managerConfigService, startedAt, serviceID),
		ManagerConfigService:    managerConfigService,
		CollectorService:        collectorService,
		UsageService:            usageService,
		DashboardService:        dashboardsvc.New(st, cfg.DashboardHourlyRollupEnabled),
		CodexInspectionService: codexinspectionsvc.NewWithOptions(
			st,
			managerConfigService,
			codexinspectionsvc.ServiceOptions{AuthFileMutationCoordinator: authFileMutationCoordinator},
		),
		MonitoringService:       monitoringsvc.New(st, cfg.DashboardHourlyRollupEnabled),
		QuotaSnapshotService:    quotasnapshotsvc.New(st),
		ModelPriceService:       modelpricesvc.NewMultiSourceWithModelsDev(st, modelsDevModelPriceSyncURL, modelPriceSyncURL, openRouterModelPriceSyncURL, managerConfigService),
		APIKeyAliasService:      apikeyaliassvc.New(st),
		ProviderKeyAliasService: providerkeyaliassvc.New(st),
		AccountActionService: accountactionsvc.NewWithMutationCoordinator(
			st,
			managerConfigService,
			authFileMutationCoordinator,
		),
		AccountProcessingPolicyService: accountProcessingPolicyService,
		ResourceCapabilityService:      identityService,
		AuthFileMutationCoordinator:    authFileMutationCoordinator,
		ProxyService: proxysvc.NewWithMutationCoordinator(
			managerConfigService,
			authFileMutationCoordinator,
			st,
		),
		PanelService:             panelsvc.New(cfg.PanelPath, embeddedPanel),
		AutomationRuntimeService: runtimeService,
	}
	legacyGuard := &managementquery.LegacyGuard{Budget: managementBudget, Identity: identityService}
	out.ProxyService.LegacyQuotaGuard = legacyGuard
	out.CodexInspectionService.LegacyQuotaGuard = legacyGuard
	// Fresh services bind once at startup; source admission remains in76.
	coordinator := out.FullRefreshService.(*fullrefreshsvc.Service)
	out.ProviderPolicyService.BindCoordinator(coordinator)
	_ = out.CredentialPolicyService.BindCoordinator(coordinator)
	out.FullRefreshService = &credentialpolicysvc.CoordinatedService{Service: coordinator, Policy: out.CredentialPolicyService, Background: []func(context.Context) error{out.ProviderPolicyService.Run}}
	return out
}
