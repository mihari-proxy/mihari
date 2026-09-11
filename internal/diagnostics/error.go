package diagnostics

import "github.com/mihari-proxy/mihari/internal/control/protocol"

// Wrap combines a public API error with its internal cause.
func Wrap(api protocol.APIError, cause error) error {
	public := protocol.APIError{
		Code:    api.Code,
		Message: api.Message,
		Details: api.Details,
	}
	return failure{api: public, cause: cause}
}

type failure struct {
	api   protocol.APIError
	cause error
}

func (e failure) Error() string { return e.api.Message }

func (e failure) Unwrap() []error {
	if e.cause == nil {
		return []error{e.api}
	}
	return []error{e.api, e.cause}
}

// MarkReported marks err as already reported by its diagnostic owner.
func MarkReported(err error) error {
	if err == nil {
		return nil
	}
	return reported{cause: err}
}

type reported struct {
	cause error
}

func (e reported) Error() string { return e.cause.Error() }
func (e reported) Unwrap() error { return e.cause }

// AlreadyReported reports whether err carries a diagnostic ownership marker.
func AlreadyReported(err error) bool {
	const maxDepth = 32
	for depth := 0; depth <= maxDepth; depth++ {
		if err == nil {
			return false
		}
		if _, ok := err.(reported); ok {
			return true
		}
		if _, ok := err.(interface{ Unwrap() []error }); ok {
			return false
		}
		if depth == maxDepth {
			return false
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}
