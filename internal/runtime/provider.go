package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

// RefreshProvider delegates native provider refresh to mihomo through the mutation coordinator.
func (m *Manager) RefreshProvider(ctx context.Context, operation Operation, name string) error {
	_, err := m.doOperation(ctx, "rule-provider:"+operation.ID, func() (any, error) {
		if m.controller == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		_, err := m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(current state.Snapshot) (state.Snapshot, error) {
			return current, m.controller.UpdateRuleProvider(ctx, name)
		})
		return struct{}{}, err
	})
	return err
}
