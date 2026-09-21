package tui

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"log/slog"
)

func startCleanupTask(ctx context.Context, cleanup func(context.Context) error, reporter diagnostics.Reporter) func() {
	if cleanup == nil {
		return func() {}
	}
	child, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := cleanup(child); err != nil && reporter != nil && !diagnostics.NormalCancellation(child, err) {
			reporter(child, diagnostics.Record{Component: "update", Event: "startup.cleanup.failed", Summary: "Remove obsolete application binary", Level: slog.LevelWarn, Err: err})
		}
	}()
	return func() { cancel(); <-done }
}
