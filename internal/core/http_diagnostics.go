package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func coreHTTPError(api protocol.APIError, operation, address, phase string, response *http.Response, cause error) error {
	detail := &diagnostics.HTTPError{Operation: operation, URL: address, Phase: phase, Cause: cause}
	if response != nil {
		detail.Status = response.StatusCode
		if response.Body != nil && phase == "response" {
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, diagnostics.MaxHTTPBodyBytes+1))
			detail.Body = diagnostics.HTTPBody(raw)
			detail.BodyTruncated = len(raw) > diagnostics.MaxHTTPBodyBytes
			detail.Cause = errors.Join(cause, readErr)
		}
	}
	return diagnostics.Wrap(api, detail)
}

func closeCoreResponse(ctx context.Context, response *http.Response, operation, address string, resultErr *error, reporter diagnostics.Reporter) {
	if response == nil || response.Body == nil {
		return
	}
	if err := response.Body.Close(); err != nil {
		detail := &diagnostics.HTTPError{Operation: operation, URL: address, Phase: "close", Status: response.StatusCode, Cause: err}
		if *resultErr != nil {
			var api protocol.APIError
			if errors.As(*resultErr, &api) {
				*resultErr = diagnostics.Wrap(api, errors.Join(*resultErr, detail))
			} else {
				*resultErr = errors.Join(*resultErr, detail)
			}
		} else if reporter != nil {
			reporter(ctx, diagnostics.Record{Component: "core", Event: "http.close.failed", Level: slog.LevelWarn, Err: detail})
		}
	}
}

func joinCoreFailure(primary, secondary error) error {
	if secondary == nil {
		return primary
	}
	var api protocol.APIError
	if errors.As(primary, &api) {
		return diagnostics.Wrap(api, errors.Join(primary, secondary))
	}
	return errors.Join(primary, secondary)
}
