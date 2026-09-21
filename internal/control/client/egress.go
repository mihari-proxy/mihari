package client

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

// Egress returns the saved selection and current interface snapshot.
func (c *Client) Egress(ctx context.Context) (protocol.EgressStatus, error) {
	var result protocol.EgressStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/egress", nil, &result)
	return result, err
}

// UpdateEgress changes the daemon-owned outbound interface.
func (c *Client) UpdateEgress(ctx context.Context, request protocol.EgressUpdateRequest) (protocol.EgressStatus, error) {
	var result protocol.EgressStatus
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "egress.update"}, http.MethodPatch, "/v1/egress", request, &result)
	return result, err
}
