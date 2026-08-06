package runtime

import (
	"context"
	"errors"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/preferences"
	"github.com/mihari-proxy/mihari/internal/state"
)

func (m *Manager) TUIPreferences() preferences.Preferences {
	if m.preferences == nil {
		return preferences.Preferences{}
	}
	return m.preferences.Snapshot()
}

func (m *Manager) UpdateTUIPreferences(ctx context.Context, operation Operation, update preferences.Update) (preferences.Preferences, error) {
	result, err := m.doOperation(ctx, "preferences-tui:"+operation.ID, func() (any, error) {
		if m.preferences == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "TUI preferences are unavailable"}
		}
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		var updated preferences.Preferences
		_, err := m.coordinator.Do(ctx, state.CommandMeta{
			ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision,
		}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			var updateErr error
			updated, updateErr = m.preferences.Update(ctx, update)
			if updateErr != nil {
				return snapshot, preferenceMutationError(updateErr)
			}
			return snapshot, nil
		})
		if err != nil {
			return nil, err
		}
		return updated, nil
	})
	if err != nil {
		return preferences.Preferences{}, err
	}
	return result.(preferences.Preferences), nil
}

func preferenceMutationError(err error) error {
	if errors.Is(err, preferences.ErrInvalidColumns) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid TUI connections columns"}
	}
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist TUI preferences"}
}
