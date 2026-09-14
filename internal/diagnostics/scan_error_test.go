package diagnostics

import "testing"

type scanNilWrapper struct{ cause error }

func (e *scanNilWrapper) Error() string { return e.cause.Error() }
func (e *scanNilWrapper) Unwrap() error { return e.cause }

func TestAlreadyReported_TypedNilDoesNotInvokeUnwrap(t *testing.T) {
	var err *scanNilWrapper
	if AlreadyReported(err) {
		t.Fatal("nil wrapper was marked reported")
	}
}
