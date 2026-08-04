package runtime

import (
	"context"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/onboarding"
	"github.com/LeeShunEE/mihari/internal/state"
)

func (m *Manager) OnboardingStatus(ctx context.Context) (onboarding.Snapshot, error) {
	if m.onboarding == nil {
		return onboarding.Snapshot{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "onboarding service is unavailable"}
	}
	if err := m.lock(ctx); err != nil {
		return onboarding.Snapshot{}, err
	}
	defer m.unlock()
	return onboarding.Snapshot{Status: m.onboarding.Status(), Revision: m.store.Load().Revision}, nil
}

func (m *Manager) UpdateOnboarding(ctx context.Context, operation Operation, update onboarding.Update) (onboarding.Snapshot, error) {
	result, err := m.doOperation(ctx, "onboarding:"+operation.ID, func() (any, error) {
		if m.onboarding == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "onboarding service is unavailable"}
		}
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		var updated onboarding.Status
		committed, err := m.coordinator.Do(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			var updateErr error
			updated, updateErr = m.onboarding.Update(update)
			return snapshot, updateErr
		})
		if err != nil {
			return nil, err
		}
		return onboarding.Snapshot{Status: updated, Revision: committed.Revision}, nil
	})
	if err != nil {
		return onboarding.Snapshot{}, err
	}
	return result.(onboarding.Snapshot), nil
}
