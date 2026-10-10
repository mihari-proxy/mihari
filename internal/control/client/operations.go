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

// ObserveOperationProgress reads presentation-only progress from an operation observer.
// Client transport failures are returned without recording each poll in diagnostic history.
// Settlement callers should use OperationStatus, which retains ordinary failure reporting.
func ObserveOperationProgress(ctx context.Context, observer interface {
	OperationStatus(context.Context, string) (protocol.OperationStatus, error)
}, id string) (protocol.OperationStatus, error) {
	if c, ok := observer.(*Client); ok {
		var result protocol.OperationStatus
		outcome := c.doRuntimeOutcome(ctx, http.MethodGet, "/v1/operations/"+url.PathEscape(id), nil, &result, maxControlResponseSize)
		return result, outcome.err
	}
	return observer.OperationStatus(ctx, id)
}
