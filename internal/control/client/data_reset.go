package client

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"net/http"
)

// DataResetPreview reads the exact resource-preserving reset plan.
func (c *Client) DataResetPreview(ctx context.Context) (protocol.ResetPreview, error) {
	var v protocol.ResetPreview
	err := c.doRuntime(ctx, http.MethodGet, "/v1/data-reset", nil, &v)
	return v, err
}

// ResetBusinessData sends both explicit confirmations for a previously reviewed plan.
func (c *Client) ResetBusinessData(ctx context.Context, r protocol.DataResetRequest) (protocol.DataResetResult, error) {
	var v protocol.DataResetResult
	err := c.doMutation(ctx, logging.OperationMetadata{ID: r.OperationID, Name: "data-reset"}, http.MethodPost, "/v1/data-reset", r, &v)
	return v, err
}
