package client

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
	"net/url"
)

// CheckCoreVersion queries upstream metadata through the daemon.
func (c *Client) CheckCoreVersion(ctx context.Context) (protocol.VersionCheck, error) {
	var result protocol.VersionCheck
	err := c.doRuntime(ctx, http.MethodGet, "/v1/core/version-check", nil, &result)
	return result, err
}

// CheckPanelVersion queries a panel's upstream build through the daemon.
func (c *Client) CheckPanelVersion(ctx context.Context, id string) (protocol.VersionCheck, error) {
	var result protocol.VersionCheck
	err := c.doRuntime(ctx, http.MethodGet, "/v1/panels/"+url.PathEscape(id)+"/version-check", nil, &result)
	return result, err
}
