package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

func TestLogging_UnconfirmedCompensationFencesWrites(t *testing.T) {
	m, c := onlineLoggingManager(t)
	c.failReadAfterPatch = true
	if _, err := m.UpdateLogging(t.Context(), Operation{ID: "unconfirmed"}, LoggingUpdate{Level: stringPointer("debug")}); err == nil {
		t.Fatal("unconfirmed change succeeded")
	}
	if m.Snapshot().Health != "degraded" || !m.mutationDegraded.Load() {
		t.Fatal("missing recovery fence")
	}
	if _, err := m.UpdateLogging(t.Context(), Operation{ID: "blocked"}, LoggingUpdate{MaxFiles: int64Pointer(4)}); err == nil {
		t.Fatal("degraded manager accepted mutation")
	}
	status, err := m.LoggingStatus(t.Context())
	if err != nil || status.CoreLevel != "" || status.SyncState != "unknown" {
		t.Fatalf("invented confirmation: %+v %v", status, err)
	}
}

func TestLogging_ValidationDoesNotHoldMutationAndRejectsStaleGeneration(t *testing.T) {
	m, c := onlineLoggingManager(t)
	entered, release := make(chan struct{}), make(chan struct{})
	m.validateConfig = func(ctx context.Context, _ string) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.UpdateLogging(ctx, Operation{ID: "stale"}, LoggingUpdate{Level: stringPointer("debug")})
		done <- err
	}()
	<-entered
	updateCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	_, err := m.UpdateLogging(updateCtx, Operation{ID: "rotation"}, LoggingUpdate{MaxFiles: int64Pointer(4)})
	close(release)
	if err != nil {
		<-done
		t.Fatal(err)
	}
	assertLoggingAPIError(t, <-done, protocol.CodeRevisionConflict)
	if c.patches != 0 || c.reloads != 0 {
		t.Fatal("stale candidate reached core")
	}
}

func TestLogging_CancellationAfterSaveKeepsCommittedOnlineChange(t *testing.T) {
	m, c := onlineLoggingManager(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.saveSettings = func(path string, settings config.Settings) (config.CommitResult, error) {
		result, err := config.SaveWithCommit(path, settings)
		cancel()
		return result, err
	}
	op := Operation{ID: "committed"}
	status, err := m.UpdateLogging(ctx, op, LoggingUpdate{Level: stringPointer("debug")})
	if err != nil || status.Level != "debug" || c.level != "debug" {
		t.Fatalf("committed cancellation: %+v %v", status, err)
	}
	_, err = m.UpdateLogging(t.Context(), op, LoggingUpdate{Level: stringPointer("debug")})
	if err != nil || c.patches != 1 || c.reloads != 1 {
		t.Fatal("replayed core mutation after cancellation")
	}
}

func TestLogging_SilentRotationAndStoppedChangeDoNotMutateCore(t *testing.T) {
	m, c := onlineLoggingManager(t)
	c.level = "silent"
	if err := m.SyncLogging(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateLogging(t.Context(), Operation{ID: "rotation"}, LoggingUpdate{MaxFiles: int64Pointer(4)}); err != nil {
		t.Fatal(err)
	}
	if m.logging.Config().Level != logging.LevelSilent || c.patches != 0 || c.reloads != 0 {
		t.Fatal("rotation left silent or changed core")
	}
	m.Observe(supervisor.Observation{Status: supervisor.StatusStopped})
	m.validateConfig = func(context.Context, string) error { t.Error("stopped change validated a core candidate"); return nil }
	status, err := m.UpdateLogging(t.Context(), Operation{ID: "stopped"}, LoggingUpdate{Level: stringPointer("warn")})
	if err != nil || status.SyncState != "pending" || status.Level != "warn" || c.patches != 0 {
		t.Fatalf("stopped update=%+v %v", status, err)
	}
}
