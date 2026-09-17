package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// CheckCoreVersion reads upstream metadata for the committed core channel.
// Network work runs outside the mutation gate; no state or revision is changed.
func (m *Manager) CheckCoreVersion(ctx context.Context) (protocol.VersionCheck, error) {
	checker, ok := m.installer.(interface {
		LatestVersion(context.Context, string) (string, error)
	})
	if !ok {
		return protocol.VersionCheck{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core version check is unavailable"}
	}
	channel := m.settingsSnapshot().CoreChannel
	if channel == "" {
		channel = "stable"
	}
	latest, err := checker.LatestVersion(ctx, channel)
	if err != nil {
		return protocol.VersionCheck{}, err
	}
	return protocol.VersionCheck{Schema: "mihari/v1", Latest: latest, Channel: channel}, nil
}

// CheckPanelVersion reads metadata for a supported panel, including uninstalled ones.
func (m *Manager) CheckPanelVersion(ctx context.Context, id string) (protocol.VersionCheck, error) {
	checker, ok := m.panels.(interface {
		LatestBuild(context.Context, string) (string, error)
	})
	if !ok {
		return protocol.VersionCheck{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel version check is unavailable"}
	}
	latest, err := checker.LatestBuild(ctx, id)
	if err != nil {
		return protocol.VersionCheck{}, err
	}
	return protocol.VersionCheck{Schema: "mihari/v1", Latest: latest}, nil
}
