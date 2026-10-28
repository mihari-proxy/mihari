package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"time"
)

// BusinessResetStore is the daemon's fixed-list persistence boundary for a confirmed reset.
type BusinessResetStore interface {
	Preview(context.Context) (protocol.ResetPreview, error)
	Reset(context.Context, string) (config.Settings, string, error)
	Pending() (bool, error)
	Complete() error
}

// DataResetPreview returns exact files before either confirmation or any mutation.
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

// ResetBusinessData stops the owned core, resets exact business files, and leaves Setup reachable over IPC.
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
		_, canStop := m.supervisor.(interface {
			Update(context.Context, func(*supervisor.UpdateSession) error) error
		})
		if !canStop && (m.store.Load().Core.PID != 0 || m.running.Load() || m.coreRunRequested.Load()) {
			m.unlock()
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core cannot be stopped for reset"}
		}
		m.unlock()
		if err := m.ClearOwnedSystemProxy(ctx); err != nil {
			return nil, err
		}
		work := func(session *supervisor.UpdateSession) error {
			if session != nil {
				session.KeepStopped()
			}
			ownerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			defer cancel()
			if err := m.lockMaintenance(ownerCtx); err != nil {
				return err
			}
			defer m.unlock()
			settings, token, err := m.businessReset.Reset(ownerCtx, request.Digest)
			if err != nil {
				pending, pendingErr := m.businessReset.Pending()
				if pending || pendingErr != nil {
					m.dataResetPending.Store(true)
					m.mutationDegraded.Store(true)
					m.onboardingRestartRequired.Store(true)
				}
				return errors.Join(err, pendingErr)
			}
			m.dataResetPending.Store(true)
			m.onboardingRestartRequired.Store(true)
			before := m.settingsSnapshot()
			m.publishSettings(settingsCandidate{before: before, after: settings, changed: true})
			m.settingsMu.Lock()
			m.webOpenToken = token
			m.settingsMu.Unlock()
			if m.subscriptions != nil {
				if err := m.subscriptions.Restore(subscription.Defaults()); err != nil {
					m.mutationDegraded.Store(true)
					return err
				}
			}
			if m.preferences != nil {
				if _, err := m.preferences.Reset(ownerCtx); err != nil {
					m.mutationDegraded.Store(true)
					return err
				}
			}
			if m.onboarding != nil {
				complete := false
				if _, err := m.onboarding.Update(&complete); err != nil {
					m.mutationDegraded.Store(true)
					return err
				}
			}
			m.loggingUnsaved = false
			m.loggingObservation = loggingObservation{}
			m.mutationDegraded.Store(false)
			if m.logging != nil {
				cfg, err := loggingConfig(settings.EffectiveLogging())
				if err != nil {
					return err
				}
				m.logging.Apply(ownerCtx, cfg)
			}
			_, err = m.updateStateLocked(ownerCtx, state.CommandMeta{ID: op.ID, Source: op.Source}, func(s state.Snapshot) (state.Snapshot, error) {
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
			m.refreshSubscriptionLogSecrets()
			return err
		}
		if owner, ok := m.supervisor.(interface {
			Update(context.Context, func(*supervisor.UpdateSession) error) error
		}); ok {
			err = owner.Update(ctx, work)
		} else {
			err = work(nil)
		}
		if m.dataResetPending.Load() {
			if gateway, ok := m.webGateway.(interface{ Shutdown(context.Context) error }); ok {
				shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				err = errors.Join(err, gateway.Shutdown(shutdown))
				cancel()
			}
		}
		if err != nil {
			return nil, err
		}
		return protocol.DataResetResult{Schema: "mihari/v1", Revision: m.store.Load().Revision, SetupRequired: true, RestartRequired: true}, nil
	})
	if err != nil {
		return protocol.DataResetResult{}, err
	}
	return value.(protocol.DataResetResult), nil
}
func (m *Manager) completeResetSetup(ctx context.Context, complete *bool) error {
	if complete == nil || !*complete || !m.dataResetPending.Load() {
		return nil
	}
	if err := m.lockMutation(ctx); err != nil {
		return err
	}
	if !m.dataResetPending.Load() {
		m.unlock()
		return nil
	}
	if m.onboardingRestartRequired.Load() {
		m.unlock()
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "restart the daemon after data reset before completing Setup"}
	}
	if m.businessReset == nil {
		m.unlock()
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "reset recovery store unavailable"}
	}
	if err := m.businessReset.Complete(); err != nil {
		m.unlock()
		return err
	}
	m.dataResetPending.Store(false)
	m.unlock()
	if owner, ok := m.supervisor.(interface {
		Update(context.Context, func(*supervisor.UpdateSession) error) error
	}); ok {
		return owner.Update(ctx, func(session *supervisor.UpdateSession) error {
			if err := session.Start(ctx); err != nil {
				session.KeepStopped()
				return err
			}
			return nil
		})
	}
	return nil
}
