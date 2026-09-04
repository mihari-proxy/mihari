package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/preferences"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"github.com/mihari-proxy/mihari/internal/tundetect"
)

type PreparedCore = core.PreparedCore

type CoreInstaller interface {
	Prepare(context.Context, core.InstallRequest) (core.PreparedCore, error)
	DetectVersion(context.Context, string) (string, error)
}

type CoreSupervisor interface {
	Run(context.Context) error
	Restart(context.Context) error
}

type Controller interface {
	Proxies(context.Context) (mihomo.Proxies, error)
	SelectProxy(context.Context, string, string) error
	DelayGroup(context.Context, string, string, int) (mihomo.Delays, error)
	DelayProxy(context.Context, string, string, int) (uint16, error)
	Connections(context.Context) (mihomo.Connections, error)
	CloseConnection(context.Context, string) error
	CloseAllConnections(context.Context) error
	Rules(context.Context) (mihomo.Rules, error)
	RuleProviders(context.Context) (mihomo.RuleProviders, error)
	UpdateRuleProvider(context.Context, string) error
	Configs(context.Context) (map[string]any, error)
	PatchConfigs(context.Context, map[string]any) error
	Stream(context.Context, mihomo.StreamKind, func(json.RawMessage) error) error
}

type Operation struct {
	ID         string
	Source     string
	IfRevision *uint64
	Channel    *string
}

// GeoIPCandidate is the minimal prepared-pair contract used by the mutation coordinator.
type GeoIPCandidate interface {
	Identity() string
	Valid() bool
	Commit() error
	Cleanup()
}

// GeoIPService is the local lookup and health boundary used by the runtime.
type GeoIPService interface {
	Status() geoip.Status
	Lookup(netip.Addr) (geoip.Record, error)
}

type Options struct {
	Store          *state.Store
	Coordinator    *state.Coordinator
	Installer      CoreInstaller
	InstallRequest core.InstallRequest
	Supervisor     CoreSupervisor
	Controller     Controller
	BinaryExists   func() bool
	Subscriptions  *subscription.Service
	Preferences    *preferences.Service
	Settings       config.Settings
	RuntimeConfig  string
	StagingDir     string
	ValidateConfig func(context.Context, string) error
	RunScheduler   func(context.Context) error
	GeoIP          GeoIPService
	PrepareGeoIP   func(context.Context) (GeoIPCandidate, error)
	Onboarding     *onboarding.Service
	Panels         PanelService
	// WebGateway is the optional loopback browser gateway. Failures do not stop the core supervisor.
	WebGateway WebGateway
	// WebOpenToken is the Web access credential used only to mint open-browser URLs for local clients.
	// It must never appear in status DTOs, logs, or default CLI output.
	WebOpenToken string
	// SysProxy is the OS system-proxy backend. Nil installs the platform default in New.
	SysProxy sysproxy.Backend
	// TunDetect is the TUN conflict detection backend. Nil installs the platform default in New.
	TunDetect tundetect.Backend
	// LookupTCPOccupant reports the PID listening on a host:port. Nil installs
	// platform.LookupTCPOccupant. Tests inject a fake to avoid real socket tables.
	LookupTCPOccupant func(string) (int, bool)
	// SettingsPath is where config.Save writes settings after system-proxy (and related) mutations.
	// Empty skips persistence (in-memory settings only).
	SettingsPath string
	// SaveSettings persists an independently prepared settings candidate.
	// Nil uses config.SaveWithCommit.
	SaveSettings func(string, config.Settings) (config.CommitResult, error)
	// ServiceStatus reports the OS service registration state for onboarding review.
	// Optional; nil reports "unknown". Injected as a func (not *service.Manager) to keep
	// runtime free of the service package and break the main↔daemon assembly cycle.
	ServiceStatus func() (string, error)
	// OnBackgroundError receives non-cancellation failures from the web gateway
	// and owned scheduler. Optional; nil keeps the previous discard behavior.
	OnBackgroundError func(component string, err error)
}

// WebGateway is the loopback HTTP server for panel hosting and API proxying.
type WebGateway interface {
	Serve(context.Context) error
	SessionCount() int
	ListenAddr() string
}

type Manager struct {
	store             *state.Store
	coordinator       *state.Coordinator
	installer         CoreInstaller
	installRequest    core.InstallRequest
	supervisor        CoreSupervisor
	controller        Controller
	binaryExists      func() bool
	subscriptions     *subscription.Service
	preferences       *preferences.Service
	settings          config.Settings
	runtimeConfig     string
	stagingDir        string
	validateConfig    func(context.Context, string) error
	runScheduler      func(context.Context) error
	geoip             GeoIPService
	prepareGeoIP      func(context.Context) (GeoIPCandidate, error)
	onboarding        *onboarding.Service
	panels            PanelService
	webGateway        WebGateway
	webOpenToken      string
	sysProxy          sysproxy.Backend
	tunDetect         tundetect.Backend
	lookupOccupant    func(string) (int, bool)
	settingsPath      string
	saveSettings      func(string, config.Settings) (config.CommitResult, error)
	serviceStatus     func() (string, error)
	onBackgroundError func(component string, err error)
	settingsMu        sync.RWMutex
	tunLastError      string
	maintenance       chan struct{}
	installed         chan struct{}
	closing           atomic.Bool
	mutationDegraded  atomic.Bool
	running           atomic.Bool
	operationsMu      sync.Mutex
	operations        map[string]*operationEntry
}

type operationEntry struct {
	done   chan struct{}
	result any
	err    error
}

func New(options Options) *Manager {
	store := options.Store
	if store == nil {
		store = state.NewStore(state.Snapshot{Health: "ok"})
	}
	coordinator := options.Coordinator
	if coordinator == nil {
		coordinator = state.NewCoordinator(store)
	}
	binaryExists := options.BinaryExists
	if binaryExists == nil {
		binaryExists = func() bool { return true }
	}
	sysProxy := options.SysProxy
	if sysProxy == nil {
		sysProxy = sysproxy.Platform()
	}
	tunDetect := options.TunDetect
	if tunDetect == nil {
		tunDetect = tundetect.Platform()
	}
	lookupOccupant := options.LookupTCPOccupant
	if lookupOccupant == nil {
		lookupOccupant = func(addr string) (int, bool) {
			occ, ok := platform.LookupTCPOccupant(addr)
			if !ok {
				return 0, false
			}
			return occ.PID, true
		}
	}
	saveSettings := options.SaveSettings
	if saveSettings == nil {
		saveSettings = config.SaveWithCommit
	}
	settings := options.Settings.Clone()
	if settings.Schema == "" {
		settings = config.Defaults()
	}
	manager := &Manager{
		store:             store,
		coordinator:       coordinator,
		installer:         options.Installer,
		installRequest:    options.InstallRequest,
		supervisor:        options.Supervisor,
		controller:        options.Controller,
		binaryExists:      binaryExists,
		subscriptions:     options.Subscriptions,
		preferences:       options.Preferences,
		settings:          settings,
		runtimeConfig:     options.RuntimeConfig,
		stagingDir:        options.StagingDir,
		validateConfig:    options.ValidateConfig,
		runScheduler:      options.RunScheduler,
		geoip:             options.GeoIP,
		prepareGeoIP:      options.PrepareGeoIP,
		onboarding:        options.Onboarding,
		panels:            options.Panels,
		webGateway:        options.WebGateway,
		webOpenToken:      options.WebOpenToken,
		sysProxy:          sysProxy,
		tunDetect:         tunDetect,
		lookupOccupant:    lookupOccupant,
		settingsPath:      options.SettingsPath,
		saveSettings:      saveSettings,
		serviceStatus:     options.ServiceStatus,
		onBackgroundError: options.OnBackgroundError,
		maintenance:       make(chan struct{}, 1),
		installed:         make(chan struct{}, 1),
		operations:        make(map[string]*operationEntry),
	}
	manager.maintenance <- struct{}{}
	if manager.subscriptions != nil {
		snapshot := manager.store.Load()
		manager.syncSubscriptionState(&snapshot, manager.subscriptions.Snapshot())
		manager.store.Store(snapshot)
	}
	return manager
}

func (m *Manager) Run(ctx context.Context) error {
	defer m.closing.Store(true)
	if closer, ok := m.geoip.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	if m.webGateway != nil {
		webDone := make(chan struct{})
		go func() {
			defer close(webDone)
			// Panel/gateway failure must not stop mihomo supervision.
			m.reportBackground("web-gateway", m.webGateway.Serve(ctx))
		}()
		defer func() { <-webDone }()
	}
	if m.runScheduler != nil {
		schedulerCtx, cancelScheduler := context.WithCancel(ctx)
		schedulerDone := make(chan struct{})
		go func() {
			defer close(schedulerDone)
			m.reportBackground("scheduler", m.runScheduler(schedulerCtx))
		}()
		defer func() {
			cancelScheduler()
			<-schedulerDone
		}()
	}
	shutdownObserved := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			m.closing.Store(true)
		case <-shutdownObserved:
		}
	}()
	defer close(shutdownObserved)
	if m.supervisor == nil {
		m.setCoreState(state.CoreState{Status: "degraded", LastError: "mihomo supervisor is unavailable"})
		<-ctx.Done()
		return nil
	}
	for !m.binaryExists() {
		m.setCoreState(state.CoreState{Status: "missing"})
		select {
		case <-ctx.Done():
			return nil
		case <-m.installed:
		}
	}
	m.running.Store(true)
	// Best-effort restore of desired OS system proxy; failures must not block core supervision.
	_ = m.ApplyDesiredSystemProxy(ctx)
	defer func() { _ = m.ClearOwnedSystemProxy(context.Background()) }()
	err := m.supervisor.Run(ctx)
	m.running.Store(false)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		m.setCoreState(state.CoreState{Status: "degraded", LastError: "mihomo supervisor stopped"})
	}
	return err
}

func (m *Manager) reportBackground(component string, err error) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if m.onBackgroundError == nil {
		return
	}
	m.onBackgroundError(component, err)
}

func (m *Manager) Observe(observation supervisor.Observation) {
	current := m.store.Load().Core
	m.setCoreState(state.CoreState{
		Status:      string(observation.Status),
		PID:         observation.PID,
		Restarts:    observation.Restarts,
		LastError:   observation.LastError,
		NextRetryAt: observation.NextRetryAt,
		Version:     current.Version,
		Channel:     current.Channel,
		AlphaSHA:    current.AlphaSHA,
	})
}

func (m *Manager) Snapshot() state.Snapshot { return m.store.Load() }

// BrowserSessions returns the approximate concurrent authenticated Web gateway sessions.
func (m *Manager) BrowserSessions() int {
	if m.webGateway == nil {
		return 0
	}
	return m.webGateway.SessionCount()
}

// WebListenAddr returns the bound Web gateway address when available.
func (m *Manager) WebListenAddr() string {
	settings := m.settingsSnapshot()
	if m.webGateway == nil {
		return settings.WebAddr
	}
	if addr := m.webGateway.ListenAddr(); addr != "" {
		return addr
	}
	return settings.WebAddr
}

func (m *Manager) Proxies(ctx context.Context) (mihomo.Proxies, error) {
	if m.controller == nil {
		return mihomo.Proxies{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
	}
	return m.controller.Proxies(ctx)
}

func (m *Manager) DelayGroup(ctx context.Context, group, testURL string, timeoutMilliseconds int) (mihomo.Delays, error) {
	if m.controller == nil {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
	}
	return m.controller.DelayGroup(ctx, group, testURL, timeoutMilliseconds)
}

func (m *Manager) DelayProxy(ctx context.Context, name, testURL string, timeoutMilliseconds int) (uint16, error) {
	if m.controller == nil {
		return 0, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
	}
	return m.controller.DelayProxy(ctx, name, testURL, timeoutMilliseconds)
}

func (m *Manager) Connections(ctx context.Context) (mihomo.Connections, error) {
	if m.controller == nil {
		return mihomo.Connections{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
	}
	return m.controller.Connections(ctx)
}

func (m *Manager) Rules(ctx context.Context) (mihomo.Rules, error) {
	if m.controller == nil {
		return mihomo.Rules{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
	}
	return m.controller.Rules(ctx)
}

func (m *Manager) RuleProviders(ctx context.Context) (mihomo.RuleProviders, error) {
	if m.controller == nil {
		return mihomo.RuleProviders{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
	}
	return m.controller.RuleProviders(ctx)
}

func (m *Manager) UpdateRuleProvider(ctx context.Context, operation Operation, name string) error {
	_, err := m.doOperation(ctx, "rule-provider:"+operation.ID, func() (any, error) {
		if m.controller == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		_, err := m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			if updateErr := m.controller.UpdateRuleProvider(ctx, name); updateErr != nil {
				return snapshot, updateErr
			}
			return snapshot, nil
		})
		return struct{}{}, err
	})
	return err
}

func (m *Manager) Stream(ctx context.Context, kind mihomo.StreamKind, receive func(json.RawMessage) error) error {
	if m.controller == nil {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
	}
	return m.controller.Stream(ctx, kind, receive)
}

func (m *Manager) Install(ctx context.Context, operation Operation) (core.InstallResult, error) {
	result, err := m.doOperation(ctx, "install:"+operation.ID, func() (any, error) {
		if m.installer == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core installer is unavailable"}
		}
		channel := m.settingsSnapshot().CoreChannel
		if channel == "" {
			channel = "stable"
		}
		if operation.Channel != nil {
			channel = *operation.Channel
		}
		if channel != "stable" && channel != "alpha" {
			return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid core channel"}
		}
		installRequest := m.installRequest
		installRequest.CurrentVersion = m.store.Load().Core.Version
		installRequest.Channel = channel
		installRequest.AlphaSHA = m.store.Load().Core.AlphaSHA
		// setup 预检（design §4.3）：aio 脚本已预置核心时，对现有文件 -v 成功即秒过，不联网。
		// store.Core.Version 不作判据（DetectVersion 失败时旧值残留，见 runtime.go 启动检测）。
		if operation.Source == "setup" {
			if version, detectErr := m.installer.DetectVersion(ctx, installRequest.BinaryPath); detectErr == nil && version != "" {
				err := func() error {
					if err := m.lockMutation(ctx); err != nil {
						return err
					}
					defer m.unlock()
					if err := ctx.Err(); err != nil {
						return err
					}
					if err := m.checkIfRevision(operation.IfRevision); err != nil {
						return err
					}

					sidecar := filepath.Join(filepath.Dir(installRequest.BinaryPath), "core-channel")
					candidate, err := m.prepareSettings(func(settings *config.Settings) error {
						_, applyErr := config.ApplyCoreChannelSidecar(settings, sidecar)
						return applyErr
					})
					if err != nil {
						return err
					}
					if err := ctx.Err(); err != nil {
						return err
					}
					if !candidate.changed {
						return nil
					}
					if _, err := m.saveSettingsCandidate(candidate); err != nil {
						return err
					}
					m.publishSettings(candidate)
					_, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{
						ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision,
					}, func(snapshot state.Snapshot) (state.Snapshot, error) {
						snapshot.Core.Version = version
						snapshot.Core.Channel = candidate.after.CoreChannel
						return snapshot, nil
					})
					return err
				}()
				if err != nil {
					return nil, err
				}
				return core.InstallResult{Version: version, Updated: false}, nil
			}
			// -v 失败（缺失/损坏）→ 落 Prepare 联网修复（失败由 Prepare 报错并提示 aio 脚本）。
		}
		candidate, err := m.installer.Prepare(ctx, installRequest)
		if err != nil {
			return nil, err
		}
		defer candidate.Cleanup()
		var result core.InstallResult
		if err := func() error {
			if err := m.lockMutation(ctx); err != nil {
				return err
			}
			defer m.unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := m.checkIfRevision(operation.IfRevision); err != nil {
				return err
			}

			candidateUpdated := candidate.Updated()
			result, err = candidate.Commit()
			if err != nil {
				return err
			}
			if candidateUpdated {
				if _, err := m.updateSettings(func(settings *config.Settings) error {
					settings.CoreChannel = channel
					return nil
				}); err != nil {
					return err
				}
				_, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{
					ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision,
				}, func(snapshot state.Snapshot) (state.Snapshot, error) {
					snapshot.Core.Version = result.Version
					snapshot.Core.Channel = channel
					snapshot.Core.AlphaSHA = result.AlphaSHA
					if channel == "stable" {
						snapshot.Core.AlphaSHA = ""
					}
					return snapshot, nil
				})
			}
			return err
		}(); err != nil {
			return nil, err
		}
		if !result.Updated {
			return result, nil
		}
		if m.running.Load() {
			if err := m.supervisor.Restart(ctx); err != nil {
				return nil, err
			}
		} else {
			select {
			case m.installed <- struct{}{}:
			default:
			}
		}
		return result, nil
	})
	if err != nil {
		return core.InstallResult{}, err
	}
	return result.(core.InstallResult), nil
}

// LocalCore reports whether the configured core binary already satisfies setup
// locally (mihomo -v succeeds), so onboarding can hint "use existing" without a
// network install. Read-only: no lock, no store mutation. Mirrors the Install
// setup fast-path predicate (design §4.3), DRY — same DetectVersion judgment.
func (m *Manager) LocalCore(ctx context.Context) (core.LocalCoreInfo, error) {
	if m.installer == nil {
		return core.LocalCoreInfo{}, nil
	}
	version, err := m.installer.DetectVersion(ctx, m.installRequest.BinaryPath)
	if err != nil || version == "" {
		return core.LocalCoreInfo{}, nil
	}
	return core.LocalCoreInfo{Ready: true, Version: version}, nil
}

// ServiceStatus reports the OS service registration state for onboarding review.
// Advisory: a nil injector or any error resolves to "unknown" and never fails, so the
// endpoint stays 200 and onboarding is never blocked (design §5.3, §8).
func (m *Manager) ServiceStatus(context.Context) (protocol.ServiceStatus, error) {
	if m.serviceStatus == nil {
		return protocol.ServiceStatus{Schema: "mihari/v1", Status: "unknown"}, nil
	}
	status, err := m.serviceStatus()
	if err != nil || status == "" {
		return protocol.ServiceStatus{Schema: "mihari/v1", Status: "unknown"}, nil
	}
	return protocol.ServiceStatus{Schema: "mihari/v1", Status: status}, nil
}

func (m *Manager) Restart(ctx context.Context, operation Operation) error {
	_, err := m.doOperation(ctx, "restart:"+operation.ID, func() (any, error) {
		if m.supervisor == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo is not running"}
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		m.unlock()
		if err := m.supervisor.Restart(ctx); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}

func (m *Manager) SelectProxy(ctx context.Context, operation Operation, group, name string) error {
	_, err := m.doOperation(ctx, "select:"+operation.ID, func() (any, error) {
		if m.controller == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
		}
		if err := m.withControllerMutation(ctx, operation, func() error { return m.controller.SelectProxy(ctx, group, name) }); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}

func (m *Manager) CloseConnection(ctx context.Context, operation Operation, id string) error {
	_, err := m.doOperation(ctx, "close:"+operation.ID, func() (any, error) {
		if m.controller == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
		}
		if err := m.withControllerMutation(ctx, operation, func() error { return m.controller.CloseConnection(ctx, id) }); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}

func (m *Manager) CloseAllConnections(ctx context.Context, operation Operation) error {
	_, err := m.doOperation(ctx, "close-all:"+operation.ID, func() (any, error) {
		if m.controller == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
		}
		if err := m.withControllerMutation(ctx, operation, func() error { return m.controller.CloseAllConnections(ctx) }); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}

func (m *Manager) withControllerMutation(ctx context.Context, operation Operation, mutation func() error) error {
	return m.withMaintenance(ctx, func() error {
		if operation.IfRevision != nil {
			current := m.store.Load().Revision
			if *operation.IfRevision != current {
				return protocol.APIError{
					Code:    protocol.CodeRevisionConflict,
					Message: "state revision changed",
					Details: map[string]any{
						"expected_revision": *operation.IfRevision,
						"current_revision":  current,
					},
				}
			}
		}
		if err := mutation(); err != nil {
			return err
		}
		_, err := m.updateStateLocked(ctx, state.CommandMeta{
			ID:         operation.ID,
			Source:     operation.Source,
			IfRevision: operation.IfRevision,
		}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			return snapshot, nil
		})
		return err
	})
}

func (m *Manager) withMaintenance(ctx context.Context, operation func() error) error {
	if err := m.lockMutation(ctx); err != nil {
		return err
	}
	defer m.unlock()
	if err := m.checkOpen(); err != nil {
		return err
	}
	return operation()
}

func (m *Manager) unlock() { m.maintenance <- struct{}{} }

func (m *Manager) checkOpen() error {
	if m.closing.Load() {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihari daemon is shutting down"}
	}
	return nil
}

func (m *Manager) doOperation(ctx context.Context, key string, execute func() (any, error)) (any, error) {
	if err := m.checkOpen(); err != nil {
		return nil, err
	}
	if key == "" || key[len(key)-1] == ':' {
		return execute()
	}
	m.operationsMu.Lock()
	if existing := m.operations[key]; existing != nil {
		m.operationsMu.Unlock()
		select {
		case <-existing.done:
			return existing.result, existing.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	entry := &operationEntry{done: make(chan struct{})}
	if len(m.operations) >= 256 {
		for operationKey, operation := range m.operations {
			select {
			case <-operation.done:
				delete(m.operations, operationKey)
			default:
			}
			if len(m.operations) < 256 {
				break
			}
		}
		if len(m.operations) >= 256 {
			m.operationsMu.Unlock()
			return execute()
		}
	}
	m.operations[key] = entry
	m.operationsMu.Unlock()

	entry.result, entry.err = execute()
	close(entry.done)
	return entry.result, entry.err
}

func (m *Manager) setCoreState(coreState state.CoreState) {
	if m.lockMaintenance(context.Background()) != nil {
		return
	}
	defer m.unlock()
	_, _ = m.updateStateLocked(context.Background(), state.CommandMeta{Source: "runtime"}, func(snapshot state.Snapshot) (state.Snapshot, error) {
		snapshot.Core = coreState
		return snapshot, nil
	})
}
