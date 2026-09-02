package runtime

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/preferences"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

// ResetUserData clears user-owned disk state and returns the daemon to first-run
// setup. The OS service, Mihari binary, control token, core binary, GeoIP
// databases, and installed panel builds are kept so the TUI can stay connected
// and caches do not need to be re-downloaded.
func (m *Manager) ResetUserData(ctx context.Context, op Operation) (protocol.DataResetResult, error) {
	result, err := m.doOperation(ctx, "data-reset:"+op.ID, func() (any, error) {
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		if op.IfRevision != nil {
			current := m.store.Load().Revision
			if *op.IfRevision != current {
				return nil, protocol.APIError{
					Code:    protocol.CodeRevisionConflict,
					Message: "state revision changed",
					Details: map[string]any{
						"expected_revision": *op.IfRevision,
						"current_revision":  current,
					},
				}
			}
		}

		if err := m.ClearOwnedSystemProxy(ctx); err != nil {
			return nil, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "disable system proxy before reset"}
		}

		m.settingsMu.Lock()
		secret := m.settings.ControllerSecret
		coreChannel := m.settings.CoreChannel
		coreBundle := m.settings.CoreChannelBundle
		next := config.Defaults()
		next.ControllerSecret = secret
		if coreChannel != "" {
			next.CoreChannel = coreChannel
		}
		next.CoreChannelBundle = coreBundle
		m.settings = next
		saveErr := m.persistSettings()
		m.settingsMu.Unlock()
		if saveErr != nil {
			return nil, saveErr
		}

		if m.onboarding != nil {
			complete := false
			mixed, controller, web := next.MixedAddr, next.ControllerAddr, next.WebAddr
			if _, err := m.onboarding.Update(onboarding.Update{
				Complete: &complete, MixedAddr: &mixed, ControllerAddr: &controller, WebAddr: &web,
			}); err != nil {
				return nil, err
			}
			m.onboarding.ReplaceSettings(next)
			m.settingsMu.Lock()
			m.settings = next
			saveErr = m.persistSettings()
			m.settingsMu.Unlock()
			if saveErr != nil {
				return nil, saveErr
			}
		}

		if m.subscriptions != nil {
			if _, _, err := m.subscriptions.Mutate(func(catalog *subscription.Catalog) error {
				*catalog = subscription.Defaults()
				return nil
			}); err != nil {
				return nil, err
			}
		}
		if err := replaceDir(m.paths.SubscriptionCache); err != nil {
			return nil, dataResetFileError("clear subscription cache")
		}
		if m.paths.TUIPreferences != "" {
			_ = os.Remove(m.paths.TUIPreferences)
			service, err := preferences.Open(m.paths.TUIPreferences)
			if err != nil {
				return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "reset TUI preferences"}
			}
			m.preferences = service
		}
		if m.paths.WebActive != "" {
			if err := os.Remove(m.paths.WebActive); err != nil && !os.IsNotExist(err) {
				return nil, dataResetFileError("clear panel activation")
			}
		}
		if err := truncateFile(m.paths.Log); err != nil {
			return nil, dataResetFileError("clear logs")
		}
		if err := replaceDir(m.paths.Staging); err != nil {
			return nil, dataResetFileError("clear staging")
		}
		if m.paths.Root != "" {
			if err := m.paths.EnsureDirs(); err != nil {
				return nil, dataResetFileError("restore data directories")
			}
		}

		if m.runtimeConfig != "" {
			content, err := core.BootstrapConfig(m.settings)
			if err != nil {
				return nil, err
			}
			if err := config.AtomicWrite(m.runtimeConfig, content, 0o600); err != nil {
				return nil, dataResetFileError("write bootstrap runtime configuration")
			}
		}
		if m.supervisor != nil {
			if err := m.supervisor.Restart(ctx); err != nil {
				return nil, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "restart core after reset"}
			}
		}

		committed, err := m.coordinator.Do(ctx, state.CommandMeta{
			ID: op.ID, Source: op.Source, IfRevision: op.IfRevision,
		}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			if m.subscriptions != nil {
				m.syncSubscriptionState(&snapshot, m.subscriptions.Snapshot())
			}
			return snapshot, nil
		})
		if err != nil {
			return nil, err
		}
		return protocol.DataResetResult{
			Schema: "mihari/v1", OperationID: op.ID, Revision: committed.Revision, SetupRequired: true,
		}, nil
	})
	if err != nil {
		return protocol.DataResetResult{}, err
	}
	return result.(protocol.DataResetResult), nil
}

func replaceDir(path string) error {
	if path == "" {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o700)
}

func truncateFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func dataResetFileError(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}
