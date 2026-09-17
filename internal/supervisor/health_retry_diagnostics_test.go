package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestMonitor_ReportsEachRetryAndRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiter := newFakeWaiter()
	recorder := newSupervisorDiagnosticRecorder()
	causes := []error{os.ErrPermission, os.ErrInvalid, nil}
	attempt := 0
	s := New(Options{Waiter: waiter, DiagnosticReporter: recorder.report, Health: func(context.Context) error {
		err := causes[attempt]
		attempt++
		return err
	}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.monitor(ctx, 42, 0, time.Time{}, make(chan error, 1))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("health monitor failed to stop")
		}
	})
	for range 3 {
		waiter.next(t).release()
	}
	// The next wait proves the third observation and its diagnostics finished.
	waiter.next(t)
	records := recorder.snapshot()
	if len(records) != 3 {
		t.Fatalf("health diagnostics=%d want=3", len(records))
	}
	for i := range 2 {
		if records[i].record.Event != "core.health.retry" || records[i].record.Level != slog.LevelWarn || !errors.Is(records[i].record.Err, causes[i]) {
			t.Fatalf("retry %d lost original cause: %+v", i+1, records[i])
		}
	}
	if records[2].record.Event != "core.health.recovered" || records[2].record.Level != slog.LevelInfo {
		t.Fatalf("recovery missing: %+v", records[2])
	}
}
