package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// OperationStatus observes settlement without executing or replaying a mutation.
func (c *Client) OperationStatus(ctx context.Context, id string) (protocol.OperationStatus, error) {
	var result protocol.OperationStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/operations/"+url.PathEscape(id), nil, &result)
	return result, err
}
