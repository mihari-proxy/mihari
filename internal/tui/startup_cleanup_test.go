package tui

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"strings"
	"testing"
	"time"
)

// TestStartupCleanup_ReportsWithoutFileLogger verifies independent F2 reporting and owned completion.
func TestStartupCleanup_ReportsWithoutFileLogger(t *testing.T) {
	failure := errors.New("remove old binary: fixture denied")
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "cleanup-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	owner := diagnostics.NewOwner(history, nil)
	records := make(chan diagnostics.Record, 1)
	ran := make(chan struct{})
	stop := startCleanupTask(context.Background(), func(context.Context) error { close(ran); return failure }, func(ctx context.Context, r diagnostics.Record) { owner.Report(ctx, r); records <- r })
	defer stop()
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("cleanup never started")
	}
	select {
	case r := <-records:
		if !errors.Is(r.Err, failure) {
			t.Fatal(r.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup failure missing from diagnostics")
	}
	entries := history.List("", 0, 10)
	if len(entries.Records) != 1 {
		t.Fatalf("F2 history=%+v", entries)
	}
	detail := history.Get(entries.Records[0].ID)
	if detail.Diagnostic == nil || !strings.Contains(detail.Diagnostic.Detail, failure.Error()) {
		t.Fatalf("original F2 detail missing: %+v", detail)
	}
}

// TestStartupCleanup_CancelJoins ensures logging is not closed while cleanup still reports.
func TestStartupCleanup_CancelJoins(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	stop := startCleanupTask(context.Background(), func(ctx context.Context) error { close(started); <-ctx.Done(); defer close(finished); return ctx.Err() }, nil)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cleanup never started")
	}
	stop()
	stop()
	select {
	case <-finished:
	default:
		t.Fatal("cleanup not joined")
	}
}
