package client

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"net/http"
)

// ProxyAuth reads saved and effective proxy users. Reveal must be explicitly requested.
func (c *Client) ProxyAuth(ctx context.Context, reveal bool) (protocol.ProxyAuthStatus, error) {
	path := "/v1/proxy-auth"
	if reveal {
		path += "?reveal=true"
	}
	var result protocol.ProxyAuthStatus
	err := c.doRuntime(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

// UpdateProxyAuth replaces instance proxy authentication overrides.
func (c *Client) UpdateProxyAuth(ctx context.Context, request protocol.ProxyAuthUpdateRequest) (protocol.ProxyAuthStatus, error) {
	var result protocol.ProxyAuthStatus
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "proxy-auth.update"}, http.MethodPut, "/v1/proxy-auth", request, &result)
	return result, err
}
