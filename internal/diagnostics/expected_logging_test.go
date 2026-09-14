package diagnostics

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestExpectedLogging_RecordsRejectionsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{context.Canceled, Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "canceled"}, context.Canceled), protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "revision conflict"}} {
		if level, emit := FailureLevel(ctx, err); !emit || level != slog.LevelInfo {
			t.Fatalf("expected INFO event, got %v %v", level, emit)
		}
	}
	if level, emit := FailureLevel(ctx, errors.Join(context.Canceled, os.ErrPermission)); !emit || level != slog.LevelError {
		t.Fatal("cancellation hid an actual IO failure")
	}
}
