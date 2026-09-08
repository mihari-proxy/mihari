package client

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
)

// GetInstallationStatus reads installation completeness through authenticated local control.
func (c *Client) GetInstallationStatus(ctx context.Context) (protocol.InstallationStatus, error) {
	var result protocol.InstallationStatus
	err := c.doRuntimeLimit(ctx, http.MethodGet, "/v1/install/status", nil, &result, 4096)
	return result, err
}
