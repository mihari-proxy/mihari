package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// RefreshProvider delegates native provider refresh to mihomo through the mutation coordinator.
func (m *Manager) RefreshProvider(ctx context.Context, operation Operation, name string) error {
	_, err := m.doOperation(ctx, "rule-provider:"+operation.ID, func(ctx context.Context) (any, error) {
		if m.controller == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
		}
		err := m.withControllerMutation(ctx, operation, func() error {
			return m.controller.UpdateRuleProvider(ctx, name)
		})
		return struct{}{}, err
	})
	return err
}
