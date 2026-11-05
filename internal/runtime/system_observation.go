package runtime

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/observation"
	"github.com/mihari-proxy/mihari/internal/preferences"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"github.com/mihari-proxy/mihari/internal/tundetect"
)

type systemObservationKey struct {
	generation, revision, configGeneration uint64
	policy                                 preferences.PerformancePreferences
	scope                                  string
}
type tunObservation struct {
	detection tundetect.Detection
	self      tundetect.Self
	configs   map[string]any
}
type egressObservation struct {
	state      string
	interfaces []protocol.EgressInterface
	failure    error
}
type systemObservations struct {
	generation atomic.Uint64
	tun        observation.Cache[systemObservationKey, tunObservation]
	egress     observation.Cache[systemObservationKey, egressObservation]
	proxy      observation.Cache[systemObservationKey, sysproxy.State]
	service    observation.Cache[systemObservationKey, string]
}
type displayObservationKey struct{}
type displayProbe struct {
	failure error
}

func recordDisplayFailure(ctx context.Context, err error) {
	if p, ok := ctx.Value(displayObservationKey{}).(*displayProbe); ok && err != nil {
		p.failure = errors.Join(p.failure, err)
	}
}

func (m *Manager) observationKey(scope string) systemObservationKey {
	policy := preferences.DefaultPerformancePreferences()
	if m.preferences != nil {
		policy = m.preferences.Snapshot().Performance
	}

	return systemObservationKey{generation: m.systemObservations.generation.Load(), revision: m.store.Load().Revision, configGeneration: m.currentConfigGeneration(), policy: policy, scope: scope}
}
func (m *Manager) invalidateSystemObservations() {
	m.systemObservations.generation.Add(1)
	m.systemObservations.tun.Invalidate()
	m.systemObservations.egress.Invalidate()
	m.systemObservations.proxy.Invalidate()
	m.systemObservations.service.Invalidate()
}
func (m *Manager) closeSystemObservations() {
	m.systemObservations.tun.Close()
	m.systemObservations.egress.Close()
	m.systemObservations.proxy.Close()
	m.systemObservations.service.Close()
}

// readSystemObservation joins before acquiring mutation ownership. Probes and
// final projection each verify identity under maintenance ownership; mutations
// never consume these caches.
func readSystemObservation[R, D any](m *Manager, ctx context.Context, cache *observation.Cache[systemObservationKey, R], scope func() (string, error), probe func(context.Context, systemObservationKey) (R, error), project func(R, error) (D, error)) (D, error) {
	var zero D
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if scope == nil {
		scope = func() (string, error) { return "", nil }
	}
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		identity, err := scope()
		if err != nil {
			return zero, err
		}
		key := m.observationKey(identity)
		value, probeErr := cache.Get(ctx, key, time.Duration(key.policy.ObservationReuseSeconds)*time.Second, func(worker context.Context) (R, error) {
			var empty R
			if observation.Fresh(ctx) {
				worker = observation.WithFresh(worker)
			}
			if err := m.lockMaintenance(worker); err != nil {
				return empty, err
			}
			defer m.unlock()
			currentScope, err := scope()
			if err != nil {
				return empty, err
			}
			if key != m.observationKey(currentScope) {
				return empty, observation.ErrInvalidated
			}
			worker = context.WithValue(worker, displayObservationKey{}, &displayProbe{})
			return probe(worker, key)
		})
		if errors.Is(probeErr, observation.ErrInvalidated) {
			continue
		}
		if errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
			return zero, probeErr
		}
		if err = m.lockMaintenance(ctx); err != nil {
			return zero, err
		}
		currentScope, err := scope()
		if err != nil {
			m.unlock()
			return zero, err
		}
		if key != m.observationKey(currentScope) {
			m.unlock()
			continue
		}
		out, err := project(value, probeErr)
		m.unlock()
		return out, err
	}
}

// Each new top-level probe captures live configs. A completed nested cache
// would extend data age and could defeat an explicit refresh that joins it.
func (m *Manager) captureDisplayConfigs(ctx context.Context) (map[string]any, error) {
	if m.controller == nil {
		return nil, nil
	}
	raw, err := m.controller.Configs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any)
	if tun, ok := raw["tun"].(map[string]any); ok {
		out["tun"] = cloneTunMap(tun)
	}
	if value, ok := raw["interface-name"]; ok {
		out["interface-name"] = value
	}
	return out, nil
}

func (m *Manager) displayTunStatus(ctx context.Context) (protocol.TunStatus, error) {
	return readSystemObservation(m, ctx, &m.systemObservations.tun, nil, func(worker context.Context, key systemObservationKey) (tunObservation, error) {
		var out tunObservation
		var detectErr, errorOwner error
		if m.tunDetect != nil {
			out.detection, detectErr = m.tunDetect.Detect(worker)
			out.detection.TunInterfaces = slices.Clone(out.detection.TunInterfaces)
			out.detection.MihomoProcesses = slices.Clone(out.detection.MihomoProcesses)
		}
		out.self = tundetect.Self{CorePID: m.store.Load().Core.PID, DaemonPID: os.Getpid(), BinaryPath: m.installRequest.BinaryPath}
		if detectErr == nil && m.controller != nil && key.policy.ControllerOwnerDetection && m.lookupOccupant != nil {
			if pid, ok := m.lookupOccupant(m.settingsSnapshot().ControllerAddr); ok {
				out.self.OccupantPID = pid
			} else {
				// The legacy bool cannot distinguish absent evidence from an IO
				// failure. Keep the display contract, but never cache certainty.
				errorOwner = errors.New("controller owner could not be confirmed")
			}
		}
		configs, configErr := m.captureDisplayConfigs(worker)
		out.configs = configs
		if live, ok := liveTunEnable(configs); ok && live {
			out.self.TunActive = true
			out.self.TunName = liveTunDevice(configs)
		}
		if detectErr != nil {
			out.detection = tundetect.Detection{}
		}
		return out, errors.Join(detectErr, configErr, errorOwner)
	}, func(out tunObservation, _ error) (protocol.TunStatus, error) {
		var live *bool
		if enabled, ok := liveTunEnable(out.configs); ok {
			live = &enabled
		}
		conflict := tundetect.Classify(out.detection, out.self)
		if conflict != nil {
			conflict.OtherTunInterfaces = slices.Clone(conflict.OtherTunInterfaces)
		}
		m.settingsMu.RLock()
		lastError := m.tunLastError
		m.settingsMu.RUnlock()
		return buildTunStatusFromObservation(m.settingsSnapshot(), m.store.Load().Revision, conflict, live, lastError), nil
	})
}

func (m *Manager) displayEgressStatus(ctx context.Context) (protocol.EgressStatus, error) {
	return readSystemObservation(m, ctx, &m.systemObservations.egress, nil, func(worker context.Context, _ systemObservationKey) (egressObservation, error) {
		status, err := m.egressStatus(worker)
		failure := err
		if p, ok := worker.Value(displayObservationKey{}).(*displayProbe); ok {
			err = errors.Join(err, p.failure)
		}
		return egressObservation{state: status.State, interfaces: status.Interfaces, failure: failure}, err
	}, func(out egressObservation, err error) (protocol.EgressStatus, error) {
		if out.failure != nil {
			return protocol.EgressStatus{}, out.failure
		}
		adapters := slices.Clone(out.interfaces)
		for i := range adapters {
			adapters[i].Addresses = slices.Clone(adapters[i].Addresses)
		}
		return protocol.EgressStatus{Schema: "mihari/v1", Revision: m.store.Load().Revision, Selection: egressSelection(m.settingsSnapshot().EgressInterface), State: out.state, Interfaces: adapters}, nil
	})
}
func (m *Manager) egressConfigs(ctx context.Context) (map[string]any, error) {
	value, err := m.controller.Configs(ctx)
	recordDisplayFailure(ctx, err)
	return value, err
}

func (m *Manager) displaySystemProxyStatus(ctx context.Context) (protocol.SystemProxyStatus, error) {
	scope := func() (string, error) {
		if backend, ok := m.sysProxy.(interface{ ObservationScope() (string, error) }); ok {
			value, err := backend.ObservationScope()
			if err != nil {
				return "", diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "read system proxy state"}, err)
			}
			return value, nil
		}
		return "", nil
	}
	return readSystemObservation(m, ctx, &m.systemObservations.proxy, scope, func(worker context.Context, key systemObservationKey) (sysproxy.State, error) {
		if m.sysProxy == nil {
			return sysproxy.State{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "system proxy backend is unavailable"}
		}
		var out sysproxy.State
		var err error
		if backend, ok := m.sysProxy.(interface {
			GetContext(context.Context) (sysproxy.State, error)
		}); ok {
			out, err = backend.GetContext(worker)
		} else {
			out, err = m.sysProxy.Get()
		}
		if err != nil {
			return out, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "read system proxy state"}, err)
		}
		current, err := scope()
		if err != nil {
			return out, err
		}
		if current != key.scope {
			return out, observation.ErrInvalidated
		}
		return out, nil
	}, func(out sysproxy.State, err error) (protocol.SystemProxyStatus, error) {
		if err != nil {
			return protocol.SystemProxyStatus{}, err
		}
		desired, mixed := m.systemProxySettings()
		target, _, _, err := resolveSystemProxyTarget(mixed)
		if err != nil {
			return protocol.SystemProxyStatus{}, err
		}
		return m.buildSystemProxyStatus(desired, target, out, ""), nil
	})
}
