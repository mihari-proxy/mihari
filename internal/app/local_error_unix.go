//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

// ClassifyUnixLocalError translates native local-operation failures at the app
// and process boundary. Already classified errors and cancellation keep their
// semantics; filesystem paths and credentials never become public messages.
func ClassifyUnixLocalError(err error) error {
	if err == nil {
		return nil
	}
	var api protocol.APIError
	if errors.As(err, &api) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	api = protocol.APIError{Code: protocol.CodeDataFailure, Message: "local data operation failed"}
	switch {
	case errors.Is(err, platform.ErrLeaseConflict):
		api = protocol.APIError{Code: protocol.CodeInvalidState, Message: "local instance is busy"}
	case errors.Is(err, os.ErrPermission):
		api = protocol.APIError{Code: protocol.CodePermissionDenied, Message: "local operation is not permitted"}
	case errors.Is(err, os.ErrInvalid):
		api = protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid local operation input"}
	}
	return unixLocalError{api: api, cause: err}
}

type unixLocalError struct {
	api   protocol.APIError
	cause error
}

func (e unixLocalError) Error() string   { return e.api.Error() }
func (e unixLocalError) Unwrap() []error { return []error{e.api, e.cause} }
