package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func replacementWarnings(ctx context.Context, message string) protocol.WarningOutcome {
	if message == "" {
		return protocol.WarningOutcome{}
	}
	snapshot := diagnostics.Describe(ctx, diagnostics.Record{Component: "cli", Event: "replacement.warning", Level: slog.LevelWarn, Summary: message, Err: errors.New(message)})
	return protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: snapshot.Summary, Diagnostic: &snapshot}}}
}

func renderWarnings(writer io.Writer, outcome protocol.WarningOutcome) error {
	for _, warning := range outcome.Warnings {
		snapshot := protocol.Diagnostic{Code: warning.Code, Summary: warning.Message, State: protocol.DiagnosticUnavailable}
		if warning.Diagnostic != nil {
			snapshot = *warning.Diagnostic
		}
		if snapshot.Summary == "" {
			snapshot.Summary = warning.Message
		}
		if _, err := io.WriteString(writer, diagnostics.TerminalText("Warning", snapshot)); err != nil {
			return err
		}
	}
	if outcome.WarningsOmitted > 0 {
		_, err := fmt.Fprintf(writer, "Warning: %d additional warnings exceeded the collection limit.\n", outcome.WarningsOmitted)
		return err
	}
	return nil
}
