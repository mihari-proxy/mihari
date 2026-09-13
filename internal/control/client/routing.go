package client

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"net/http"
)

// Routing returns saved intent and confirmed live routing state.
func (c *Client) Routing(ctx context.Context) (protocol.RoutingStatus, error) {
	var result protocol.RoutingStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/routing", nil, &result)
	return result, err
}

// UpdateRouting changes the daemon-owned mode without closing connections.
func (c *Client) UpdateRouting(ctx context.Context, request protocol.RoutingUpdateRequest) (protocol.RoutingStatus, error) {
	ctx = logging.WithOperation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "routing.update"})
	var result protocol.RoutingStatus
	err := c.doRuntime(ctx, http.MethodPatch, "/v1/routing", request, &result)
	return result, err
}
