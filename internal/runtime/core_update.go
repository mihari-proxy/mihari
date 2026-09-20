package runtime

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

type coreUpdateReservation struct{ generation uint64 }
type coreUpdateContextKey struct{}

type coreUpdateInputs struct {
	generation uint64
	selection  core.CoreSelection
}

func (m *Manager) ownsCoreUpdate(ctx context.Context) bool {
	reservation := m.coreUpdate.Load()
	return reservation != nil && ctx.Value(coreUpdateContextKey{}) == reservation
}

func (m *Manager) checkCoreUpdateReservation(ctx context.Context) error {
	if (m.coreUpdate.Load() != nil || m.coreRecovery.Load()) && !m.ownsCoreUpdate(ctx) {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "core update or recovery is in progress"}
	}
	return nil
}

func (m *Manager) captureCoreUpdateInputs(ctx context.Context) (coreUpdateInputs, error) {
	if err := m.lockMutation(ctx); err != nil {
		return coreUpdateInputs{}, err
	}
	defer m.unlock()
	settings, generation := m.configInputs()
	current := m.store.Load().Core
	channel := settings.CoreChannel
	if channel == "" {
		channel = "stable"
	}
	return coreUpdateInputs{generation: generation, selection: core.CoreSelection{
		Channel: channel, Bundle: settings.CoreChannelBundle, Version: current.Version, AlphaSHA: current.AlphaSHA,
	}}, nil
}

func (m *Manager) installCoreUpdate(ctx context.Context, operation Operation, inputs coreUpdateInputs, candidate PreparedCore, prepared core.PreparedUpdate, channel string, reinstall bool) (core.InstallResult, error) {
	lifecycle, ok := m.supervisor.(interface {
		Update(context.Context, func(*supervisor.UpdateSession) error) error
	})
	if !ok {
		return core.InstallResult{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core update supervision unavailable"}
	}
	runUpdate := lifecycle.Update
	if reinstall {
		repair, ok := m.supervisor.(interface {
			Reinstall(context.Context, func(*supervisor.UpdateSession) error) error
		})
		if !ok {
			return core.InstallResult{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core reinstall supervision unavailable"}
		}
		runUpdate = repair.Reinstall
	}
	if err := m.lockMutation(ctx); err != nil {
		return core.InstallResult{}, err
	}
	err := m.checkIfRevision(operation.IfRevision)
	if err == nil && inputs.generation != m.currentConfigGeneration() {
		err = protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "configuration changed during core preparation"}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		m.unlock()
		return core.InstallResult{}, err
	}
	reservation := &coreUpdateReservation{generation: inputs.generation}
	m.coreUpdate.Store(reservation)
	m.releaseMutation()
	defer m.coreUpdate.CompareAndSwap(reservation, nil)
	ctx = context.WithValue(ctx, coreUpdateContextKey{}, reservation)
	next := core.CoreSelection{Channel: channel, Bundle: inputs.selection.Bundle, Version: candidate.Version()}
	if channel == "alpha" {
		next.AlphaSHA = strings.TrimPrefix(next.Version, "alpha-")
	}
	var result core.InstallResult
	err = runUpdate(ctx, func(session *supervisor.UpdateSession) error {
		// Once maintenance accepts the operation, compensation remains owned
		// even when the requesting client cancels or disconnects.
		recoveryCtx, cancelRecovery := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer cancelRecovery()
		intent := core.UpdateIntent{Previous: inputs.selection, Next: next, WasRunning: session.WasRunning(), StartNew: m.coreRunRequested.Load()}
		begin := prepared.BeginUpdate
		if reinstall {
			repair, ok := prepared.(interface {
				BeginReinstall(context.Context, core.UpdateIntent) (*core.UpdateTransaction, error)
			})
			if !ok {
				return m.blockCoreUpdate(recoveryCtx, errors.New("core reinstall candidate unavailable"))
			}
			begin = repair.BeginReinstall
		}
		update, err := begin(ctx, intent)
		if err != nil {
			if reinstall {
				return m.blockCoreUpdate(recoveryCtx, err)
			}
			var api protocol.APIError
			if errors.As(err, &api) && api.Details["degraded"] == true {
				return m.blockCoreUpdate(recoveryCtx, err)
			}
			if session.WasRunning() {
				if restoreErr := session.Start(recoveryCtx); restoreErr != nil {
					return m.blockCoreUpdate(recoveryCtx, errors.Join(err, restoreErr))
				}
			} else {
				session.KeepStopped()
			}
			return err
		}
		defer func() {
			for _, warning := range update.Warnings() {
				collectWarning(ctx, "core", "update.warning", warning)
			}
		}()
		err = update.Publish(ctx)
		trialCtx := update.ExecutionContext(ctx)
		if err == nil {
			if update.Intent().StartNew {
				err = session.Start(trialCtx)
			} else {
				session.KeepStopped()
			}
		}
		if err == nil {
			err = update.Commit(trialCtx, func(selection core.CoreSelection) error {
				return m.saveCoreSelection(trialCtx, selection)
			})
		}
		if err != nil {
			updateErr := err
			if stopErr := session.Stop(); stopErr != nil {
				return m.blockCoreUpdate(recoveryCtx, update.RequireRecovery(recoveryCtx, errors.Join(updateErr, stopErr)))
			}
			if reinstall {
				return m.blockCoreUpdate(recoveryCtx, update.RequireRecovery(recoveryCtx, updateErr))
			}
			if err := update.Rollback(recoveryCtx, func(selection core.CoreSelection) error {
				return m.saveCoreSelection(recoveryCtx, selection)
			}); err != nil {
				return m.blockCoreUpdate(recoveryCtx, errors.Join(updateErr, err))
			}
			if session.WasRunning() {
				if err := session.Start(update.ExecutionContext(recoveryCtx)); err != nil {
					return m.blockCoreUpdate(recoveryCtx, update.RequireRecovery(recoveryCtx, errors.Join(updateErr, err)))
				}
			} else {
				session.KeepStopped()
			}
			if err := update.CompleteRollback(recoveryCtx); err != nil {
				return m.blockCoreUpdate(recoveryCtx, errors.Join(updateErr, err))
			}
			// Recovery health may legitimately update routing. Publish only core fields.
			if err := m.publishCoreSelection(recoveryCtx, inputs.selection, session.PID()); err != nil {
				return m.blockCoreUpdate(recoveryCtx, errors.Join(updateErr, err))
			}
			collectWarning(ctx, "core", "recovery.cleanup.warning", update.Finish(recoveryCtx))
			return updateErr
		}
		if err := m.publishCoreSelection(recoveryCtx, next, session.PID()); err != nil {
			return m.blockCoreUpdate(recoveryCtx, update.RequireRecovery(recoveryCtx, err))
		}
		collectWarning(ctx, "core", "update.cleanup.warning", update.Finish(recoveryCtx))
		if !update.HasPreviousCore() && session.PID() != 0 {
			collectWarning(ctx, "system-proxy", "restore.warning", m.ApplyDesiredSystemProxy(recoveryCtx))
		}
		if reinstall {
			m.coreRecovery.Store(false)
			select {
			case m.installed <- struct{}{}:
			default:
			}
		}
		result = core.InstallResult{Version: next.Version, AlphaSHA: next.AlphaSHA, Updated: true}
		return nil
	})
	return result, err
}

// saveCoreSelection never publishes memory before the journal commit point.
// Force persistence because an earlier save may have changed disk while memory
// intentionally retained the old channel. Preserve current non-core settings.
func (m *Manager) saveCoreSelection(ctx context.Context, selection core.CoreSelection) error {
	if err := m.lockMutation(ctx); err != nil {
		return err
	}
	candidate, err := m.prepareSettings(func(s *config.Settings) error {
		s.CoreChannel, s.CoreChannelBundle = selection.Channel, selection.Bundle
		return nil
	})
	m.releaseMutation()
	if err != nil {
		return err
	}
	candidate.changed = true
	_, err = m.saveSettingsCandidate(ctx, candidate)
	return err
}

func (m *Manager) publishCoreSelection(ctx context.Context, selection core.CoreSelection, pid int) error {
	if err := m.lockMaintenance(ctx); err != nil {
		return err
	}
	defer m.releaseMutation()
	candidate, err := m.prepareSettings(func(s *config.Settings) error {
		s.CoreChannel, s.CoreChannelBundle = selection.Channel, selection.Bundle
		return nil
	})
	if err != nil {
		return err
	}
	m.publishSettings(candidate)
	_, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{Source: "core-update"}, func(snapshot state.Snapshot) (state.Snapshot, error) {
		snapshot.Core.Version, snapshot.Core.AlphaSHA, snapshot.Core.Channel = selection.Version, selection.AlphaSHA, selection.Channel
		snapshot.Core.PID = pid
		snapshot.Core.Status = "stopped"
		if pid != 0 {
			snapshot.Core.Status = "running"
		}
		snapshot.Core.LastError = ""
		if snapshot.LastError == "Core update recovery required" && !m.mutationDegraded.Load() {
			snapshot.Health, snapshot.LastError = "ok", ""
		}
		return snapshot, nil
	})
	return err
}

func (m *Manager) blockCoreUpdate(ctx context.Context, cause error) error {
	m.coreRecovery.Store(true)
	if err := m.lockMaintenance(ctx); err == nil {
		_, stateErr := m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{Source: "core-update"}, func(s state.Snapshot) (state.Snapshot, error) {
			s.Health, s.Core.Status, s.Core.LastError = "degraded", "degraded", "Core update recovery required"
			s.LastError = "Core update recovery required"
			return s, nil
		})
		m.releaseMutation()
		cause = errors.Join(cause, stateErr)
	} else {
		cause = errors.Join(cause, err)
	}
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "core update recovery required", Details: map[string]any{"degraded": true}}, cause)
}

// Reinstall resolves a new official candidate in the last accepted channel.
// It never replays an interrupted transaction or executes an uncertain old core.
func (m *Manager) Reinstall(ctx context.Context, operation Operation) (core.InstallResult, error) {
	result, err := m.doOperation(ctx, "reinstall:"+operation.ID, func(ctx context.Context) (any, error) {
		if m.installer == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core installer unavailable"}
		}
		if err := m.lockMaintenance(ctx); err != nil {
			return nil, err
		}
		if m.coreUpdate.Load() != nil {
			m.releaseMutation()
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core update already in progress"}
		}
		if err := m.checkIfRevision(operation.IfRevision); err != nil {
			m.releaseMutation()
			return nil, err
		}
		reservation := &coreUpdateReservation{generation: m.currentConfigGeneration()}
		m.coreUpdate.Store(reservation)
		m.releaseMutation()
		defer m.coreUpdate.CompareAndSwap(reservation, nil)
		ctx = context.WithValue(ctx, coreUpdateContextKey{}, reservation)
		inputs, err := m.captureCoreUpdateInputs(ctx)
		if err != nil {
			return nil, err
		}
		var store core.ProvenanceStore
		if m.trustedCore != nil {
			store = m.trustedCore.Provenance()
		} else if provider, ok := m.installer.(interface{ UpdateStore() core.ProvenanceStore }); ok {
			store = provider.UpdateStore()
		}
		if store == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core reinstall store unavailable"}
		}
		pending, err := core.OpenUpdate(ctx, store)
		if err == nil {
			inputs.selection, _ = pending.ReinstallSelection()
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if operation.Channel != nil && *operation.Channel != inputs.selection.Channel {
			return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "reinstall uses the original core channel"}
		}
		request := m.installRequest
		request.Channel = inputs.selection.Channel
		request.CurrentVersion, request.AlphaSHA = "", ""
		candidate, err := m.installer.Prepare(ctx, request)
		if err != nil {
			return nil, err
		}
		defer candidate.Cleanup()
		if warnings, ok := candidate.(interface{ Warnings() []error }); ok {
			for _, warning := range warnings.Warnings() {
				collectWarning(ctx, "core", "prepare.warning", warning)
			}
		}
		prepared, ok := candidate.(interface{ UpdateCandidate() core.PreparedUpdate })
		if !ok || prepared.UpdateCandidate() == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core reinstall candidate unavailable"}
		}
		return m.installCoreUpdate(ctx, operation, inputs, candidate, prepared.UpdateCandidate(), request.Channel, true)
	})
	if err != nil {
		return core.InstallResult{}, err
	}
	return result.(core.InstallResult), nil
}
