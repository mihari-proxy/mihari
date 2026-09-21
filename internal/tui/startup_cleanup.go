package tui

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"log/slog"
)

func runStartupCleanup(ctx context.Context, cleanup func(context.Context) error, reporter diagnostics.Reporter) {
	if cleanup == nil {
		return
	}
	if err := cleanup(ctx); err != nil && reporter != nil {
		reporter(ctx, diagnostics.Record{Component: "tui", Event: "startup.cleanup.failed", Level: slog.LevelWarn, Err: err})
	}
}
