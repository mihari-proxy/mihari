package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/runtime"
	"strings"
	"testing"
)

func TestDiagnosticBudgetIPC_ExactBusinessLimitPreservesCommittedWarning(t *testing.T) {
	original := "sync token=fixture-budget\n" + strings.Repeat("\x00", diagnostics.MaxBytes)
	// Fake logging metadata fills the body budget without touching any path.
	settings := config.Defaults().EffectiveLogging()
	expected := protocol.LoggingStatus{Schema: "mihari/v1", Revision: 1, Level: "debug", MaxSizeMB: settings.MaxSizeMB, MaxFiles: settings.MaxFiles, SyncState: "pending", SyncMessage: "Saved; waiting for the core to start"}
	encoded, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	expected.Dir = strings.Repeat("x", (4<<20)-len(encoded)-1)
	fixture := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true, Warning: errors.New(original)}, nil, func(options *runtime.Options) {
		options.Logging.(*ipcLoggingRuntime).dir = expected.Dir
	})
	got, err := fixture.client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "fixture-exact-budget", Level: stringPointer("debug")})
	if err != nil {
		t.Fatalf("diagnostics invalidated the committed business response: %v", err)
	}
	if got.Revision != 1 || got.Level != "debug" || got.Dir != expected.Dir || fixture.saver.CallCount() != 1 {
		t.Fatal("diagnostic transfer changed or repeated the business result")
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Diagnostic == nil {
		t.Fatal("committed warning was lost")
	}
	detail := got.Warnings[0].Diagnostic
	if detail.Detail != diagnostics.Capture(errors.New(original)).Text || !detail.Truncated || detail.ID == "" {
		t.Fatal("original bounded diagnostic was not recovered")
	}
	// The ordinary JSON body still fits, including its newline; diagnostics use a
	// separate authenticated read and do not enlarge the business body budget.
	raw := fixture.responses.At(t, 1)
	if len(raw) != 4<<20 {
		t.Fatalf("business body bytes=%d want=%d", len(raw), 4<<20)
	}
}
