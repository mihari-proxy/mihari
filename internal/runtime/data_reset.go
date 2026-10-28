package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"time"
)

// BusinessResetStore is the daemon's fixed-list reset boundary. Warnings describe committed writes.
type BusinessResetStore interface {
	Preview(context.Context) (protocol.ResetPreview, error)
	Reset(context.Context, string, func(error)) (config.Settings, string, error)
	Pending() (bool, error)
	Complete() error
}
type resetSupervisor interface {
	Update(context.Context, func(*supervisor.UpdateSession) error) error
}
type resetSetupStartKey struct{}

// DataResetPreview reads exact reset targets without mutating them.
func (m *Manager) DataResetPreview(ctx context.Context) (protocol.ResetPreview, error) {
	if m.businessReset == nil {
		return protocol.ResetPreview{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "business reset is unavailable"}
	}
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.ResetPreview{}, err
	}
	defer m.unlock()
	preview, err := m.businessReset.Preview(ctx)
	preview.Revision = m.store.Load().Revision
	return preview, err
}

// ResetBusinessData stops and joins the core before deleting exact managed business files.
func (m *Manager) ResetBusinessData(ctx context.Context, op Operation, request protocol.DataResetRequest) (protocol.DataResetResult, error) {
	if op.ID == "" || !request.Confirm || !request.ConfirmAgain || request.Digest == "" {
		return protocol.DataResetResult{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "data reset requires a preview digest and both confirmations"}
	}
	value, err := m.doOperation(ctx, "data-reset:"+op.ID, func(ctx context.Context) (any, error) {
		if m.businessReset == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "business reset is unavailable"}
		}
		reservation := &coreUpdateReservation{done: make(chan struct{})}
		if !m.coreUpdate.CompareAndSwap(nil, reservation) {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "maintenance is in progress"}
		}
		defer func() { m.coreUpdate.CompareAndSwap(reservation, nil); close(reservation.done) }()
		ctx = context.WithValue(ctx, coreUpdateContextKey{}, reservation)
		if err := m.lockMaintenance(ctx); err != nil {
			return nil, err
		}
		if !m.businessMutationAllowed() {
			m.unlock()
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "install activation is required"}
		}
		if err := m.checkIfRevision(op.IfRevision); err != nil {
			m.unlock()
			return nil, err
		}
		preview, err := m.businessReset.Preview(ctx)
		if err != nil || preview.Digest != request.Digest {
			m.unlock()
			if err != nil {
				return nil, err
			}
			return nil, routingConflict()
		}
		owner, canStop := m.supervisor.(resetSupervisor)
		if !canStop && (m.store.Load().Core.PID != 0 || m.running.Load() || m.coreRunRequested.Load()) {
			m.unlock()
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core cannot be stopped for reset"}
		}
		mixed := m.settingsSnapshot().MixedAddr
		m.unlock()
		restoreProxy := false
		if m.sysProxy != nil {
			observed, e := m.sysProxy.Get()
			if e != nil {
				return nil, e
			}
			target, _, _, e := resolveSystemProxyTarget(mixed)
			if e != nil {
				return nil, e
			}
			restoreProxy = sysproxy.IsOwned(observed, target)
		}
		if err := m.ClearOwnedSystemProxy(ctx); err != nil {
			return nil, err
		}
		restoreOriginalProxy := func() error {
			if !restoreProxy || m.sysProxy == nil {
				return nil
			}
			current, err := m.sysProxy.Get()
			if err != nil {
				return err
			}
			if current.Enabled {
				return nil
			}
			_, host, port, err := resolveSystemProxyTarget(mixed)
			if err != nil {
				return err
			}
			return m.sysProxy.Enable(host, port)
		}
		committed := false
		workCalled := false
		work := func(session *supervisor.UpdateSession) error {
			workCalled = true
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			defer cancel()
			if err := m.lockMaintenance(recovery); err != nil {
				return err
			}
			settings, token, err := m.businessReset.Reset(recovery, request.Digest, func(err error) { collectWarning(ctx, "data-reset", "persist.warning", err) })
			if err != nil {
				pending, pendingErr := m.businessReset.Pending()
				if pending || pendingErr != nil {
					m.dataResetPending.Store(true)
					m.mutationDegraded.Store(true)
					m.onboardingRestartRequired.Store(true)
				}
				m.unlock()
				if pending || pendingErr != nil {
					if session != nil {
						session.KeepStopped()
					}
					return errors.Join(err, pendingErr)
				}
				// Before the marker commit, restore the old running intent rather than
				// treating a rejected preview as consent to leave the proxy stopped.
				if session != nil {
					if session.WasRunning() {
						if e := session.Start(recovery); e != nil {
							stopErr := session.Stop()
							session.KeepStopped()
							m.mutationDegraded.Store(true)
							return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "reset not started; previous core recovery failed", Details: map[string]any{"degraded": true}}, errors.Join(err, e, stopErr))
						}
					} else {
						session.KeepStopped()
					}
				}
				return errors.Join(err, restoreOriginalProxy())
			}
			committed = true
			if session != nil {
				session.KeepStopped()
			}
			m.publishResetSnapshots(recovery, op, settings, token)
			m.unlock()
			return nil
		}
		if canStop {
			err = owner.Update(ctx, work)
		} else {
			err = work(nil)
		}
		if err != nil && !workCalled && m.store.Load().Core.Status == "running" {
			err = errors.Join(err, restoreOriginalProxy())
		}
		if m.dataResetPending.Load() {
			if gateway, ok := m.webGateway.(interface{ Shutdown(context.Context) error }); ok {
				shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				shutdownErr := gateway.Shutdown(shutdown)
				cancel()
				if committed {
					collectWarning(ctx, "data-reset", "web.shutdown.warning", shutdownErr)
				} else {
					err = errors.Join(err, shutdownErr)
				}
			}
		}
		if err != nil && !committed {
			return nil, err
		}
		if committed {
			collectWarning(ctx, "data-reset", "post-commit.warning", err)
		}
		return protocol.DataResetResult{Schema: "mihari/v1", Revision: m.store.Load().Revision, SetupRequired: true, RestartRequired: true}, nil
	})
	if err != nil {
		return protocol.DataResetResult{}, err
	}
	return value.(protocol.DataResetResult), nil
}

func (m *Manager) publishResetSnapshots(ctx context.Context, op Operation, settings config.Settings, token string) {
	m.dataResetPending.Store(true)
	m.onboardingRestartRequired.Store(true)
	before := m.settingsSnapshot()
	m.publishSettings(settingsCandidate{before: before, after: settings, changed: true})
	m.settingsMu.Lock()
	m.webOpenToken = token
	m.settingsMu.Unlock()
	if m.subscriptions != nil {
		m.subscriptions.ResetAfterDataClear()
	}
	if m.preferences != nil {
		m.preferences.ResetAfterDataClear()
	}
	if m.onboarding != nil {
		m.onboarding.ResetAfterDataClear()
	}
	m.loggingUnsaved = false
	m.loggingObservation = loggingObservation{}
	synchronized := true
	if m.logging != nil {
		cfg, err := loggingConfig(settings.EffectiveLogging())
		if err != nil {
			synchronized = false
			collectWarning(ctx, "data-reset", "logging.config.warning", err)
		} else {
			m.logging.Apply(ctx, cfg)
		}
	}
	_, err := m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{ID: op.ID, Source: op.Source}, func(s state.Snapshot) (state.Snapshot, error) {
		s.ActiveSubscription = ""
		s.Subscriptions = nil
		s.Config = state.ConfigState{}
		s.Health = "ok"
		s.LastError = ""
		s.Core.PID = 0
		s.Core.Status = "stopped"
		s.Core.StartedAt = time.Time{}
		return s, nil
	})
	if err != nil {
		m.mutationDegraded.Store(true)
		collectWarning(ctx, "data-reset", "state.publish.warning", err)
	} else {
		m.mutationDegraded.Store(!synchronized)
	}
	m.refreshSubscriptionLogSecrets()
}

// completeResetSetup runs only after onboarding commits. Later convergence
// problems are warnings with an effective incomplete Setup, never false rollbacks.
func (m *Manager) completeResetSetup(ctx context.Context, complete *bool) {
	if complete == nil || !*complete || !m.dataResetPending.Load() {
		return
	}
	warn := func(err error) { collectWarning(ctx, "data-reset", "setup.resume.warning", err) }
	reservation := &coreUpdateReservation{done: make(chan struct{})}
	if !m.coreUpdate.CompareAndSwap(nil, reservation) {
		warn(protocol.APIError{Code: protocol.CodeInvalidState, Message: "Setup saved; maintenance is in progress, retry completion"})
		return
	}
	defer func() { m.coreUpdate.CompareAndSwap(reservation, nil); close(reservation.done) }()
	ctx = context.WithValue(ctx, coreUpdateContextKey{}, reservation)
	ctx = context.WithValue(ctx, resetSetupStartKey{}, true)
	if err := m.lockMutation(ctx); err != nil {
		warn(err)
		return
	}
	if !m.dataResetPending.Load() {
		m.unlock()
		return
	}
	if m.onboardingRestartRequired.Load() || m.businessReset == nil {
		m.unlock()
		warn(protocol.APIError{Code: protocol.CodeInvalidState, Message: "Setup saved; restart the daemon to resume reset recovery"})
		return
	}
	m.unlock()
	finish := func(session *supervisor.UpdateSession) error {
		stopOnFailure := func(cause error) error {
			if session != nil {
				stopErr := session.Stop()
				session.KeepStopped()
				if stopErr != nil {
					m.mutationDegraded.Store(true)
				}
				return errors.Join(cause, stopErr)
			}
			return cause
		}
		if session != nil {
			if err := session.Start(ctx); err != nil {
				return stopOnFailure(err)
			}
		}
		if err := m.businessReset.Complete(); err != nil {
			return stopOnFailure(err)
		}
		m.dataResetPending.Store(false)
		return nil
	}
	var err error
	if owner, ok := m.supervisor.(resetSupervisor); ok {
		err = owner.Update(ctx, finish)
	} else {
		err = finish(nil)
	}
	warn(err)
}
