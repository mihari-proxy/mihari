package client

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
	"net/url"
	"time"
)

// PrepareApplicationUpdate drains the daemon before local Windows binary replacement.
func (c *Client) PrepareApplicationUpdate(ctx context.Context, id string) (protocol.ApplicationUpdatePrepared, error) {
	var result protocol.ApplicationUpdatePrepared
	input := struct {
		OperationID string `json:"operation_id"`
	}{id}
	for attempt := 0; attempt < 2; attempt++ {
		outcome := c.doRuntimeOutcome(ctx, http.MethodPost, "/v1/app-update/prepare", input, &result, maxControlResponseSize, runtimeRequestOptions{timeout: 35 * time.Second})
		var api protocol.APIError
		if !errors.As(outcome.err, &api) || api.Code != protocol.CodeInvalidState || api.Message != protocol.UpdateFreshConnectionMessage {
			return result, outcome.err
		}
	}
	return result, protocol.APIError{Code: protocol.CodeInvalidState, Message: protocol.UpdateFreshConnectionMessage}
}

// ReleaseApplicationUpdate relinquishes a preparation owned by this process.
func (c *Client) ReleaseApplicationUpdate(ctx context.Context, id string) error {
	var result protocol.MutationResult
	return c.doRuntime(ctx, http.MethodDelete, "/v1/app-update/prepare/"+url.PathEscape(id), nil, &result)
}
