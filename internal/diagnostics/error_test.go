package diagnostics

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestWrap_PreservesCauseAndPublicClassification(t *testing.T) {
	cause := &os.PathError{Op: "rename", Path: "/fixture/private.yaml", Err: os.ErrPermission}
	public := protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist settings"}
	err := Wrap(public, cause)
	var api protocol.APIError
	var pathErr *os.PathError
	if !errors.Is(err, os.ErrPermission) || !errors.As(err, &pathErr) || pathErr != cause {
		t.Fatal("cause lost")
	}
	if !errors.As(err, &api) || api.Code != public.Code || err.Error() != public.Message {
		t.Fatal("public classification changed")
	}
	if AlreadyReported(err) {
		t.Fatal("unreported failure marked reported")
	}
	marked := MarkReported(err)
	if !AlreadyReported(fmt.Errorf("operation: %w", marked)) || !errors.Is(marked, cause) {
		t.Fatal("report marker must survive contextual wrapping")
	}
	joined := errors.Join(marked, errors.New("new failure"))
	if AlreadyReported(joined) {
		t.Fatal("a reported child must not suppress a new aggregate")
	}
}

func TestWrap_OuterClassificationTakesPriorityAndDetailsStayPublic(t *testing.T) {
	inner := protocol.APIError{Code: protocol.CodePermissionDenied, Message: "inner public message"}
	details := map[string]any{"field": "level", "reason": "unsupported"}
	public := protocol.APIError{
		Code:    protocol.CodeDataFailure,
		Message: "persist settings",
		Details: details,
	}

	err := Wrap(public, inner)
	var got protocol.APIError
	if !errors.As(err, &got) {
		t.Fatal("public classification lost")
	}
	if got.Code != public.Code || got.Message != public.Message || !reflect.DeepEqual(got.Details, details) {
		t.Fatalf("public classification = %#v, want %#v", got, public)
	}
	if err.Error() != public.Message {
		t.Fatalf("Error() = %q, want safe public message %q", err.Error(), public.Message)
	}
}

func TestWrap_PreservesJoinedCauses(t *testing.T) {
	first := errors.New("first internal cause")
	second := errors.New("second internal cause")
	err := Wrap(
		protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist settings"},
		errors.Join(first, second),
	)

	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatal("joined causes lost")
	}
	if AlreadyReported(err) {
		t.Fatal("a new failure must not be reported")
	}
}

func TestWrap_NilCauseExposesOnlyPublicClassification(t *testing.T) {
	public := protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist settings"}
	err := Wrap(public, nil)
	unwrapper, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatal("wrapped error must expose its public classification as a branch")
	}
	children := unwrapper.Unwrap()
	if len(children) != 1 {
		t.Fatalf("children = %d, want only the public classification", len(children))
	}
	var got protocol.APIError
	if !errors.As(err, &got) || got.Code != public.Code {
		t.Fatal("public classification lost with nil cause")
	}
}

func TestMarkReported_NilAndCauseTraversal(t *testing.T) {
	if MarkReported(nil) != nil || AlreadyReported(nil) {
		t.Fatal("nil must remain unmarked")
	}

	cause := &os.PathError{Op: "write", Path: "/fixture/settings.yaml", Err: os.ErrPermission}
	marked := MarkReported(cause)
	var got *os.PathError
	if !errors.Is(marked, os.ErrPermission) || !errors.As(marked, &got) || got != cause {
		t.Fatal("marker changed cause traversal")
	}
	if !AlreadyReported(marked) {
		t.Fatal("direct marker not detected")
	}
}

func TestAlreadyReported_RejectsBranchedChains(t *testing.T) {
	marked := MarkReported(errors.New("reported child"))
	joined := errors.Join(marked, errors.New("new failure"))
	if AlreadyReported(fmt.Errorf("operation: %w", joined)) {
		t.Fatal("reported child in a joined error must not mark the aggregate")
	}

	wrapped := Wrap(
		protocol.APIError{Code: protocol.CodeDataFailure, Message: "new public failure"},
		marked,
	)
	if AlreadyReported(wrapped) {
		t.Fatal("reported cause in a new public failure must not transfer its marker")
	}
}

func TestAlreadyReported_BoundsSingleChainTraversal(t *testing.T) {
	shallow := error(MarkReported(errors.New("cause")))
	for range 3 {
		shallow = singleWrap{cause: shallow}
	}
	if !AlreadyReported(shallow) {
		t.Fatal("marker in a shallow single chain not detected")
	}

	atLimit := error(MarkReported(errors.New("cause")))
	for range 32 {
		atLimit = singleWrap{cause: atLimit}
	}
	if !AlreadyReported(atLimit) {
		t.Fatal("marker at depth 32 not detected")
	}

	beyondLimit := error(MarkReported(errors.New("cause")))
	for range 33 {
		beyondLimit = singleWrap{cause: beyondLimit}
	}
	if AlreadyReported(beyondLimit) {
		t.Fatal("marker beyond 32 levels must not be followed")
	}

	cycle := &cyclicError{}
	if AlreadyReported(cycle) {
		t.Fatal("unmarked cycle reported as marked")
	}
}

type singleWrap struct {
	cause error
}

func (e singleWrap) Error() string { return e.cause.Error() }
func (e singleWrap) Unwrap() error { return e.cause }

type cyclicError struct{}

func (*cyclicError) Error() string   { return "cycle" }
func (e *cyclicError) Unwrap() error { return e }
