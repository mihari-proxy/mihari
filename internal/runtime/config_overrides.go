package runtime

import (
	"context"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// ConfigOverrides returns the saved editable categories, including disabled fragments.
func (m *Manager) ConfigOverrides(ctx context.Context) (protocol.ConfigOverridesStatus, error) {
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.ConfigOverridesStatus{}, err
	}
	defer m.unlock()
	return m.configOverridesStatus(m.settingsSnapshot()), nil
}

func (m *Manager) configOverridesStatus(settings config.Settings) protocol.ConfigOverridesStatus {
	catalog := m.loggingCatalog()
	status := protocol.ConfigOverridesStatus{
		Schema:   "mihari/v1",
		Revision: m.store.Load().Revision,
		ActiveID: catalog.ActiveID,
		Config:   settings.EffectiveOverrides(),
	}
	if index := catalog.Index(catalog.ActiveID); index >= 0 {
		status.ActiveName = catalog.Profiles[index].Name
	}
	return status
}

// UpdateConfigOverrides validates a replacement for one editor and commits it through
// the same candidate, reload and recovery path as instance authentication.
func (m *Manager) UpdateConfigOverrides(ctx context.Context, op Operation, category string, req protocol.ConfigOverridesUpdateRequest) (protocol.ConfigOverridesStatus, error) {
	if op.ID == "" {
		return protocol.ConfigOverridesStatus{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "operation ID is required"}
	}
	if category != "dns" && category != "fragments" {
		return protocol.ConfigOverridesStatus{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "unknown overrides category"}
	}
	component := "config-overrides-" + category
	result, err := m.doOperation(ctx, component+":"+op.ID, func(ctx context.Context) (any, error) {
		return m.applyGeneratedSettings(ctx, op, component, func(s *config.Settings) error {
			current := s.EffectiveOverrides()
			if category == "dns" {
				if req.Fragments != nil {
					return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "DNS editor cannot update fragments"}
				}
				current.DNS = req.DNS
			} else {
				if req.DNS != nil {
					return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "fragment editor cannot update the DNS form"}
				}
				current.Fragments = req.Fragments
			}
			s.SetOverrides(current)
			return nil
		}, func(s config.Settings, _ bool) (any, error) { return m.configOverridesStatus(s), nil })
	})
	if err != nil {
		return protocol.ConfigOverridesStatus{}, err
	}
	reply := result.(protocol.ConfigOverridesStatus)
	reply.Config = reply.Config.Clone()
	return reply, nil
}
