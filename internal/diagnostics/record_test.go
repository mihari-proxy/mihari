package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestFailureLevel_ClassifiesExpectedAndActualFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, expire := context.WithDeadline(context.Background(), timeInPast())
	defer expire()

	tests := []struct {
		name      string
		ctx       context.Context
		err       error
		wantLevel slog.Level
		wantEmit  bool
	}{
		{name: "nil", ctx: context.Background(), wantEmit: false},
		{name: "canceled", ctx: canceled, err: context.Canceled, wantEmit: false},
		{name: "wrapped cancellation", ctx: canceled, err: fmt.Errorf("stop operation: %w", context.Canceled), wantEmit: false},
		{name: "public wrapper around cancellation", ctx: canceled, err: Wrap(apiFailureValue(protocol.CodeDataFailure), context.Canceled), wantEmit: false},
		{name: "expired deadline", ctx: deadline, err: context.DeadlineExceeded, wantEmit: false},
		{name: "upstream deadline", ctx: context.Background(), err: context.DeadlineExceeded, wantLevel: slog.LevelError, wantEmit: true},
		{name: "cancellation with disk failure", ctx: canceled, err: errors.Join(context.Canceled, &os.PathError{Op: "write", Path: "/private/settings.yaml", Err: os.ErrPermission}), wantLevel: slog.LevelError, wantEmit: true},
		{name: "deadline with disk failure", ctx: deadline, err: errors.Join(context.DeadlineExceeded, &os.PathError{Op: "sync", Path: "/private", Err: os.ErrPermission}), wantLevel: slog.LevelError, wantEmit: true},
		{name: "invalid argument", ctx: context.Background(), err: apiFailure(protocol.CodeInvalidArgument), wantLevel: slog.LevelDebug, wantEmit: true},
		{name: "revision conflict", ctx: context.Background(), err: apiFailure(protocol.CodeRevisionConflict), wantLevel: slog.LevelDebug, wantEmit: true},
		{name: "system proxy conflict", ctx: context.Background(), err: apiFailure(protocol.CodeSystemProxyConflict), wantLevel: slog.LevelDebug, wantEmit: true},
		{name: "system proxy not owned", ctx: context.Background(), err: apiFailure(protocol.CodeSystemProxyNotOwned), wantLevel: slog.LevelDebug, wantEmit: true},
		{name: "tun conflict", ctx: context.Background(), err: apiFailure(protocol.CodeTunConflict), wantLevel: slog.LevelDebug, wantEmit: true},
		{name: "data failure", ctx: context.Background(), err: apiFailure(protocol.CodeDataFailure), wantLevel: slog.LevelError, wantEmit: true},
		{name: "internal failure", ctx: context.Background(), err: apiFailure(protocol.CodeInternal), wantLevel: slog.LevelError, wantEmit: true},
		{name: "network failure", ctx: context.Background(), err: apiFailure(protocol.CodeNetworkFailure), wantLevel: slog.LevelError, wantEmit: true},
		{name: "upstream failure", ctx: context.Background(), err: apiFailure(protocol.CodeUpstreamFailure), wantLevel: slog.LevelError, wantEmit: true},
		{name: "unknown failure", ctx: context.Background(), err: errors.New("unexpected failure"), wantLevel: slog.LevelError, wantEmit: true},
		{name: "expected branch plus actual failure", ctx: context.Background(), err: errors.Join(apiFailure(protocol.CodeRevisionConflict), os.ErrPermission), wantLevel: slog.LevelError, wantEmit: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			level, emit := FailureLevel(test.ctx, test.err)
			if emit != test.wantEmit || (emit && level != test.wantLevel) {
				t.Fatalf("FailureLevel() = (%v, %v), want (%v, %v)", level, emit, test.wantLevel, test.wantEmit)
			}
		})
	}
}

func TestFailureLevel_UnusualErrorValuesDoNotPanic(t *testing.T) {
	for _, err := range []error{
		comparableErrorShell{cause: sliceError{parts: []string{"disk", "failure"}}},
		(*os.PathError)(nil),
	} {
		level, emit := FailureLevel(context.Background(), err)
		if !emit || level != slog.LevelError {
			t.Fatalf("FailureLevel() = (%v, %v), want (ERROR, true)", level, emit)
		}
	}
}

func TestFailureLevel_ActiveCycleIsActualFailureButSharedDAGIsNot(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	cycle := &failureLevelCycle{}
	if level, emit := FailureLevel(canceled, errors.Join(context.Canceled, cycle)); !emit || level != slog.LevelError {
		t.Fatalf("cycle plus cancellation = (%v, %v), want (ERROR, true)", level, emit)
	}

	shared := &failureLevelWrap{cause: context.Canceled}
	if level, emit := FailureLevel(canceled, errors.Join(shared, shared)); emit {
		t.Fatalf("shared completed cancellation = (%v, %v), want no record", level, emit)
	}
}

func TestFailureLevel_TraversalBoundsCountRootAndFanoutWork(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	withinDepth := error(context.Canceled)
	for range 31 {
		withinDepth = &failureLevelWrap{cause: withinDepth}
	}
	if level, emit := FailureLevel(canceled, withinDepth); emit {
		t.Fatalf("32-layer cancellation = (%v, %v), want no record", level, emit)
	}

	beyondDepth := error(context.Canceled)
	for range 32 {
		beyondDepth = &failureLevelWrap{cause: beyondDepth}
	}
	if level, emit := FailureLevel(canceled, beyondDepth); !emit || level != slog.LevelError {
		t.Fatalf("33-layer cancellation = (%v, %v), want (ERROR, true)", level, emit)
	}

	withinNodes := make([]error, 63)
	for i := range withinNodes {
		withinNodes[i] = context.Canceled
	}
	if level, emit := FailureLevel(canceled, failureLevelMulti{children: withinNodes}); emit {
		t.Fatalf("64-node repeated DAG = (%v, %v), want no record", level, emit)
	}

	wide := make([]error, 64)
	wide[63] = failureLevelPanicUnwrap{}
	if level, emit := FailureLevel(canceled, failureLevelMulti{children: wide}); !emit || level != slog.LevelError {
		t.Fatalf("fanout beyond budget = (%v, %v), want (ERROR, true)", level, emit)
	}
}

func apiFailure(code protocol.ErrorCode) error {
	return apiFailureValue(code)
}

func apiFailureValue(code protocol.ErrorCode) protocol.APIError {
	return protocol.APIError{Code: code, Message: "safe public message"}
}

func timeInPast() time.Time {
	return time.Unix(1, 0)
}

type comparableErrorShell struct {
	cause error
}

func (e comparableErrorShell) Error() string { return "wrapped failure" }
func (e comparableErrorShell) Unwrap() error { return e.cause }

type sliceError struct {
	parts []string
}

func (e sliceError) Error() string { return strings.Join(e.parts, " ") }

type failureLevelWrap struct {
	cause error
}

func (*failureLevelWrap) Error() string       { return "wrapped" }
func (e *failureLevelWrap) Unwrap() error     { return e.cause }
func (e *failureLevelCycle) Error() string    { return "cycle" }
func (e *failureLevelCycle) Unwrap() error    { return e }
func (failureLevelMulti) Error() string       { return "multiple" }
func (e failureLevelMulti) Unwrap() []error   { return e.children }
func (failureLevelPanicUnwrap) Error() string { return "unreachable" }
func (failureLevelPanicUnwrap) Unwrap() error { panic("traversed past diagnostic node budget") }

type failureLevelCycle struct{}
type failureLevelMulti struct{ children []error }
type failureLevelPanicUnwrap struct{}
