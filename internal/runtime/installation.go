package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// GetInstallationStatus reports installation observations without a mutation path.
func (m *Manager) GetInstallationStatus(ctx context.Context) (protocol.InstallationStatus, error) {
	if err := ctx.Err(); err != nil {
		return protocol.InstallationStatus{}, err
	}
	if m.installationStatus == nil {
		return protocol.InstallationStatus{Schema: "mihari.install-status/v1", Kind: "unknown", ServiceState: "unknown", Reason: "record_invalid"}, nil
	}
	return m.installationStatus(ctx)
}
