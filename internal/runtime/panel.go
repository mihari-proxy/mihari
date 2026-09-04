package runtime

import (
	"context"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/state"
)

// PanelService is the daemon-owned panel lifecycle boundary used by the mutation coordinator.
type PanelService interface {
	List() []panel.PanelInfo
	Active() (panel.Active, error)
	ActiveDir() (string, error)
	PanelDir(panelID string) (string, error)
	Install(ctx context.Context, panelID, pinBuild string) error
	PrepareUpdate(ctx context.Context, panelID string) (panel.PreparedMutation, error)
	Activate(ctx context.Context, panelID string) error
	Rollback(ctx context.Context, panelID string) error
	Uninstall(ctx context.Context, panelID string) error
	PrepareReinstall(ctx context.Context, panelID string) (panel.PreparedMutation, error)
	SetupPath(gatewayHost string) string
	SetupPathFor(panelID, gatewayHost string) string
}

// Panels returns the redacted panel catalog with install state.
func (m *Manager) Panels(context.Context) ([]panel.PanelInfo, error) {
	if m.panels == nil {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
	}
	return m.panels.List(), nil
}

// ActivePanel returns the active panel pointer without secrets.
func (m *Manager) ActivePanel(context.Context) (panel.Active, error) {
	if m.panels == nil {
		return panel.Active{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
	}
	return m.panels.Active()
}

// InstallPanel downloads and installs outside the commit section, then bumps revision.
func (m *Manager) InstallPanel(ctx context.Context, operation Operation, panelID, pinBuild string) error {
	_, err := m.doOperation(ctx, "panel-install:"+operation.ID, func() (any, error) {
		if m.panels == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
		}
		if err := m.panels.Install(ctx, panelID, pinBuild); err != nil {
			return nil, err
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		_, err := m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			// Install already committed on disk; revision publication only.
			return snapshot, nil
		})
		return struct{}{}, err
	})
	return err
}

// UpdatePanel installs a newer build when available, then bumps revision.
func (m *Manager) UpdatePanel(ctx context.Context, operation Operation, panelID string) error {
	_, err := m.doOperation(ctx, "panel-update:"+operation.ID, func() (any, error) {
		if m.panels == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
		}
		if err := m.preflightPanelMutation(ctx, operation); err != nil {
			return nil, err
		}
		candidate, err := m.panels.PrepareUpdate(ctx, panelID)
		if err != nil {
			return nil, err
		}
		defer candidate.Cleanup()
		identity := candidate.Identity()
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		if identity == "" || candidate.Identity() != identity || !candidate.Valid() {
			return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "panel update candidate changed before commit"}
		}
		_, err = m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			if candidate.Identity() != identity || !candidate.Valid() {
				return snapshot, protocol.APIError{Code: protocol.CodeDataFailure, Message: "panel update candidate changed before commit"}
			}
			if err := candidate.Commit(); err != nil {
				return snapshot, err
			}
			return snapshot, nil
		})
		return struct{}{}, err
	})
	return err
}

// ActivatePanel switches the active panel pointer under the mutation lock.
func (m *Manager) ActivatePanel(ctx context.Context, operation Operation, panelID string) error {
	_, err := m.doOperation(ctx, "panel-activate:"+operation.ID, func() (any, error) {
		if m.panels == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		_, err := m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			if err := m.panels.Activate(ctx, panelID); err != nil {
				return snapshot, err
			}
			return snapshot, nil
		})
		return struct{}{}, err
	})
	return err
}

// RollbackPanel restores the retained previous build under the mutation lock.
func (m *Manager) RollbackPanel(ctx context.Context, operation Operation, panelID string) error {
	_, err := m.doOperation(ctx, "panel-rollback:"+operation.ID, func() (any, error) {
		if m.panels == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		_, err := m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			if err := m.panels.Rollback(ctx, panelID); err != nil {
				return snapshot, err
			}
			return snapshot, nil
		})
		return struct{}{}, err
	})
	return err
}

// UninstallPanel removes all local builds for a panel under the mutation lock.
func (m *Manager) UninstallPanel(ctx context.Context, operation Operation, panelID string) error {
	_, err := m.doOperation(ctx, "panel-uninstall:"+operation.ID, func() (any, error) {
		if m.panels == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		_, err := m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			if err := m.panels.Uninstall(ctx, panelID); err != nil {
				return snapshot, err
			}
			return snapshot, nil
		})
		return struct{}{}, err
	})
	return err
}

// ReinstallPanel uninstalls then reinstalls outside/inside commit as needed, then bumps revision.
// Download runs outside the commit section after uninstall clears local state.
func (m *Manager) ReinstallPanel(ctx context.Context, operation Operation, panelID string) error {
	_, err := m.doOperation(ctx, "panel-reinstall:"+operation.ID, func() (any, error) {
		if m.panels == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
		}
		if err := m.preflightPanelMutation(ctx, operation); err != nil {
			return nil, err
		}
		candidate, err := m.panels.PrepareReinstall(ctx, panelID)
		if err != nil {
			return nil, err
		}
		defer candidate.Cleanup()
		identity := candidate.Identity()
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		if identity == "" || candidate.Identity() != identity || !candidate.Valid() {
			return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "panel reinstall candidate changed before commit"}
		}
		_, err = m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			if candidate.Identity() != identity || !candidate.Valid() {
				return snapshot, protocol.APIError{Code: protocol.CodeDataFailure, Message: "panel reinstall candidate changed before commit"}
			}
			if err := candidate.Commit(); err != nil {
				return snapshot, err
			}
			return snapshot, nil
		})
		return struct{}{}, err
	})
	return err
}

func (m *Manager) preflightPanelMutation(ctx context.Context, operation Operation) error {
	return m.withMaintenance(ctx, func() error {
		if operation.IfRevision == nil {
			return nil
		}
		current := m.store.Load().Revision
		if *operation.IfRevision == current {
			return nil
		}
		return protocol.APIError{
			Code:    protocol.CodeRevisionConflict,
			Message: "state revision changed",
			Details: map[string]any{
				"expected_revision": *operation.IfRevision,
				"current_revision":  current,
			},
		}
	})
}
