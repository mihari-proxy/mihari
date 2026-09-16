package logging

import (
	"context"
	"log/slog"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

const (
	diagnosticMaxDepth = diagnostics.MaxDepth
	diagnosticMaxNodes = diagnostics.MaxNodes
	diagnosticMaxBytes = diagnostics.MaxBytes
)

// NewDiagnosticReporter writes original internal causes to a file logger.
// The redactor argument is retained for existing owners that also use it for
// public fallback output; it does not alter file diagnostics.
func NewDiagnosticReporter(logger *slog.Logger, _ *Redactor) diagnostics.Reporter {
	if logger == nil {
		return nil
	}
	return func(ctx context.Context, record diagnostics.Record) {
		if ctx == nil {
			ctx = context.Background()
		}
		if !logger.Enabled(ctx, record.Level) {
			return
		}
		var text string
		if record.Snapshot != nil {
			text = record.Snapshot.Detail
		} else {
			text = diagnosticText(record.Err, nil)
		}
		logger.LogAttrs(ctx, record.Level, record.Event,
			slog.String("component", record.Component),
			slog.String("cause", text),
		)
	}
}

func diagnosticText(err error, _ *Redactor) string {
	return diagnostics.Capture(err).Text
}

func boundDiagnostic(text string) string { return diagnostics.BoundText(text) }
