package runtime

import (
	"context"
	"encoding/json"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/core"
	"github.com/LeeShunEE/mihari/internal/geoip"
	"github.com/LeeShunEE/mihari/internal/mihomo"
	"github.com/LeeShunEE/mihari/internal/preferences"
	"github.com/LeeShunEE/mihari/internal/state"
	"github.com/LeeShunEE/mihari/internal/subscription"
	"github.com/LeeShunEE/mihari/internal/supervisor"
)

type PreparedCore = core.PreparedCore

type CoreInstaller interface {
	Prepare(context.Context, core.InstallRequest) (core.PreparedCore, error)
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
	Stream(context.Context, mihomo.StreamKind, func(json.RawMessage) error) error
}

type Operation struct {
	ID         string
	Source     string
	IfRevision *uint64
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
}

type Manager struct {
	store          *state.Store
	coordinator    *state.Coordinator
	installer      CoreInstaller
	installRequest core.InstallRequest
	supervisor     CoreSupervisor
	controller     Controller
	binaryExists   func() bool
	subscriptions  *subscription.Service
	preferences    *preferences.Service
	settings       config.Settings
	runtimeConfig  string
	stagingDir     string
	validateConfig func(context.Context, string) error
	runScheduler   func(context.Context) error
	geoip          GeoIPService
	prepareGeoIP   func(context.Context) (GeoIPCandidate, error)
	maintenance    chan struct{}
	installed      chan struct{}
	closing        atomic.Bool
	running        atomic.Bool
	operationsMu   sync.Mutex
	operations     map[string]*operationEntry
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
	manager := &Manager{
		store:          store,
		coordinator:    coordinator,
		installer:      options.Installer,
		installRequest: options.InstallRequest,
		supervisor:     options.Supervisor,
		controller:     options.Controller,
		binaryExists:   binaryExists,
		subscriptions:  options.Subscriptions,
		preferences:    options.Preferences,
		settings:       options.Settings,
		runtimeConfig:  options.RuntimeConfig,
		stagingDir:     options.StagingDir,
		validateConfig: options.ValidateConfig,
		runScheduler:   options.RunScheduler,
		geoip:          options.GeoIP,
		prepareGeoIP:   options.PrepareGeoIP,
		maintenance:    make(chan struct{}, 1),
		installed:      make(chan struct{}, 1),
		operations:     make(map[string]*operationEntry),
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
		defer closer.Close()
	}
	if m.runScheduler != nil {
		schedulerCtx, cancelScheduler := context.WithCancel(ctx)
		schedulerDone := make(chan struct{})
		go func() {
			defer close(schedulerDone)
			_ = m.runScheduler(schedulerCtx)
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

func (m *Manager) Observe(observation supervisor.Observation) {
	m.setCoreState(state.CoreState{
		Status:      string(observation.Status),
		PID:         observation.PID,
		Restarts:    observation.Restarts,
		LastError:   observation.LastError,
		NextRetryAt: observation.NextRetryAt,
		Version:     m.store.Load().Core.Version,
	})
}

func (m *Manager) Snapshot() state.Snapshot { return m.store.Load() }

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
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		_, err := m.coordinator.Do(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
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
		installRequest := m.installRequest
		installRequest.CurrentVersion = m.store.Load().Core.Version
		candidate, err := m.installer.Prepare(ctx, installRequest)
		if err != nil {
			return nil, err
		}
		defer candidate.Cleanup()
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		var result core.InstallResult
		if candidate.Updated() {
			_, err = m.coordinator.Do(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
				result, err = candidate.Commit()
				if err != nil {
					return snapshot, err
				}
				snapshot.Core.Version = result.Version
				return snapshot, nil
			})
			if err != nil {
				return nil, err
			}
		} else {
			result, err = candidate.Commit()
			if err != nil {
				return nil, err
			}
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

func (m *Manager) Restart(ctx context.Context, operation Operation) error {
	_, err := m.doOperation(ctx, "restart:"+operation.ID, func() (any, error) {
		if m.supervisor == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo is not running"}
		}
		if err := m.withMaintenance(ctx, func() error { return m.supervisor.Restart(ctx) }); err != nil {
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
		if err := m.withMaintenance(ctx, func() error { return m.controller.SelectProxy(ctx, group, name) }); err != nil {
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
		if err := m.withMaintenance(ctx, func() error { return m.controller.CloseConnection(ctx, id) }); err != nil {
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
		if err := m.withMaintenance(ctx, func() error { return m.controller.CloseAllConnections(ctx) }); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}

func (m *Manager) withMaintenance(ctx context.Context, operation func() error) error {
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer m.unlock()
	if err := m.checkOpen(); err != nil {
		return err
	}
	return operation()
}

func (m *Manager) lock(ctx context.Context) error {
	select {
	case <-m.maintenance:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	_, _ = m.coordinator.Do(context.Background(), state.CommandMeta{Source: "runtime"}, func(snapshot state.Snapshot) (state.Snapshot, error) {
		snapshot.Core = coreState
		return snapshot, nil
	})
}
