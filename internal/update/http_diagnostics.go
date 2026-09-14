package update

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func updateHTTPDetail(response *http.Response, address, phase string, cause error) *diagnostics.HTTPError {
	if response.Request != nil && response.Request.URL != nil {
		address = response.Request.URL.String()
	}
	return &diagnostics.HTTPError{Operation: "update GET", URL: address, Phase: phase, Status: response.StatusCode, Cause: cause}
}

func updateHTTPStatus(response *http.Response, address string) error {
	raw, err := io.ReadAll(io.LimitReader(response.Body, diagnostics.MaxHTTPBodyBytes+1))
	detail := updateHTTPDetail(response, address, "response", err)
	detail.Body = diagnostics.HTTPBody(raw)
	return detail
}

func updateHTTPTransport(address string, cause error) error {
	return &diagnostics.HTTPError{Operation: "update GET", URL: address, Phase: "transport", Cause: cause}
}

func updateHTTPRead(response *http.Response, address string, cause error) error {
	return updateHTTPDetail(response, address, "read", cause)
}

// joinUpdateFailure keeps a public message while retaining independent failures.
func joinUpdateFailure(primary, secondary error) error {
	if secondary == nil {
		return primary
	}
	var api protocol.APIError
	if errors.As(primary, &api) {
		return diagnostics.Wrap(api, errors.Join(primary, secondary))
	}
	return errors.Join(primary, secondary)
}

func closeUpdateResponse(ctx context.Context, response *http.Response, address string, resultErr *error, reporter diagnostics.Reporter) {
	if err := response.Body.Close(); err != nil {
		detail := updateHTTPDetail(response, address, "close", err)
		if *resultErr != nil {
			*resultErr = joinUpdateFailure(*resultErr, detail)
		} else if reporter != nil {
			if level, emit := diagnostics.FailureLevel(ctx, detail); emit {
				reporter(ctx, diagnostics.Record{Component: "update", Event: "http.close.failed", Level: min(level, slog.LevelWarn), Err: detail})
			}
		}
	}
}
