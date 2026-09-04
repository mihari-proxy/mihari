package runtime

import (
	"context"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/state"
)

func (m *Manager) OnboardingStatus(ctx context.Context) (onboarding.Snapshot, error) {
	if m.onboarding == nil {
		return onboarding.Snapshot{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "onboarding service is unavailable"}
	}
	if err := m.lockMaintenance(ctx); err != nil {
		return onboarding.Snapshot{}, err
	}
	defer m.unlock()
	return onboarding.Snapshot{
		Status:   m.composeOnboardingStatus(m.onboarding.State()),
		Revision: m.store.Load().Revision,
	}, nil
}

func (m *Manager) UpdateOnboarding(ctx context.Context, operation Operation, update onboarding.Update) (onboarding.Snapshot, error) {
	result, err := m.doOperation(ctx, "onboarding:"+operation.ID, func() (any, error) {
		if m.onboarding == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "onboarding service is unavailable"}
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()

		meta := state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}
		if err := m.checkIfRevision(meta.IfRevision); err != nil {
			return nil, err
		}
		candidate, err := m.prepareSettings(applyEndpointUpdate(update))
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := m.saveSettingsCandidate(candidate); err != nil {
			return nil, err
		}

		updatedState, err := m.onboarding.Update(update.Complete)
		if err != nil {
			if candidate.changed {
				rollbackCandidate := settingsCandidate{
					before:  candidate.after,
					after:   candidate.before,
					changed: true,
				}
				rollback, rollbackErr := m.saveSettingsCandidate(rollbackCandidate)
				if rollbackErr != nil && !rollback.Committed {
					m.publishSettings(candidate)
					m.onboardingRestartRequired = true
					_, degradedErr := m.updateStateLocked(context.WithoutCancel(ctx), meta, func(snapshot state.Snapshot) (state.Snapshot, error) {
						degradedErr := m.enterMutationDegraded(&snapshot)
						return snapshot, degradedErr
					})
					return nil, degradedErr
				}
			}
			return nil, mapPersistError(err)
		}

		m.publishSettings(candidate)
		m.onboardingRestartRequired = m.onboardingRestartRequired || candidate.changed
		committed, err := m.updateStateLocked(context.WithoutCancel(ctx), meta, func(snapshot state.Snapshot) (state.Snapshot, error) {
			return snapshot, nil
		})
		if err != nil {
			return nil, err
		}
		return onboarding.Snapshot{
			Status:   m.composeOnboardingStatus(updatedState),
			Revision: committed.Revision,
		}, nil
	})
	if err != nil {
		return onboarding.Snapshot{}, err
	}
	return result.(onboarding.Snapshot), nil
}

func applyEndpointUpdate(update onboarding.Update) func(*config.Settings) error {
	return func(settings *config.Settings) error {
		if update.MixedAddr != nil {
			settings.MixedAddr = *update.MixedAddr
		}
		if update.ControllerAddr != nil {
			settings.ControllerAddr = *update.ControllerAddr
		}
		if update.WebAddr != nil {
			settings.WebAddr = *update.WebAddr
		}
		return nil
	}
}

func (m *Manager) composeOnboardingStatus(onboardingState onboarding.State) onboarding.Status {
	settings := m.settingsSnapshot()
	return onboarding.Status{
		Complete:        onboardingState.Complete,
		MixedAddr:       settings.MixedAddr,
		ControllerAddr:  settings.ControllerAddr,
		WebAddr:         settings.WebAddr,
		RestartRequired: m.onboardingRestartRequired,
	}
}

func mapPersistError(error) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist settings"}
}
