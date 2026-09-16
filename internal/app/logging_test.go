package app

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type recordingLoggingRuntime struct {
	recordingWebMutationRuntime
	operation runtimeapi.Operation
	update    runtimeapi.LoggingUpdate
	ctx       context.Context
}

func (r *recordingLoggingRuntime) UpdateLogging(ctx context.Context, op runtimeapi.Operation, update runtimeapi.LoggingUpdate) (protocol.LoggingStatus, error) {
	r.ctx, r.operation, r.update = ctx, op, update
	return protocol.LoggingStatus{}, r.err
}

func TestWebLoggingPatch_ConfirmedRuntimeResult(t *testing.T) {
	for _, failure := range []error{nil, errors.New("fixture reload failure")} {
		r := &recordingLoggingRuntime{recordingWebMutationRuntime: recordingWebMutationRuntime{err: failure}}
		m := webMutator{manager: r}
		err := m.ApplyConfigPatch(t.Context(), map[string]any{"log-level": "warning"})
		if !errors.Is(err, failure) {
			t.Fatalf("result=%v want=%v", err, failure)
		}
		if r.update.Level == nil || *r.update.Level != "warn" {
			t.Fatalf("update=%+v", r.update)
		}
		assertWebOperation(t, r.operation, "web-logging-")
		metadata, ok := logging.OperationFromContext(r.ctx)
		if !ok || metadata.ID != r.operation.ID || metadata.Name != "logging.update" {
			t.Fatalf("metadata=%+v", metadata)
		}
	}
}

func TestWebLoggingPatch_RejectsInvalidOrUnavailableOwner(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch map[string]any
		code  protocol.ErrorCode
	}{
		{"silent", map[string]any{"log-level": "silent"}, protocol.CodeInvalidArgument},
		{"type", map[string]any{"log-level": 42}, protocol.CodeInvalidArgument},
		{"mixed", map[string]any{"log-level": "debug", "mode": "rule"}, protocol.CodeInvalidArgument},
		{"unavailable", map[string]any{"log-level": "debug"}, protocol.CodeInvalidState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recordingWebMutationRuntime{}
			err := (webMutator{manager: r}).ApplyConfigPatch(t.Context(), tc.patch)
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != tc.code {
				t.Fatalf("err=%v", err)
			}
			if len(r.contexts) != 0 {
				t.Fatal("rejected logging patch reached another mutation")
			}
		})
	}
}
