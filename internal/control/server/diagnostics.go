package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// Request validation owns expected rejections before an execution identity
// exists. A rejection returned to a client shares its original diagnostic.
func (s *Server) reportRequestRejection(ctx context.Context, err error) {
	if s.diagnosticReporter != nil {
		s.diagnosticReporter(ctx, diagnostics.Record{Component: "control.server", Event: "request_rejected", Level: slog.LevelInfo, Err: err})
	}
}

func (s *Server) writeInvalidArgument(ctx context.Context, writer http.ResponseWriter, message string) {
	err := diagnostics.ReportError(ctx, s.diagnosticReporter, diagnostics.Record{Component: "control.server", Event: "request_rejected", Level: slog.LevelInfo, Err: protocol.APIError{Code: protocol.CodeInvalidArgument, Message: message}})
	writeControlError(writer, err)
}

func (s *Server) writeControlError(ctx context.Context, writer http.ResponseWriter, err error) {
	if !diagnostics.AlreadyReported(err) {
		if level, report := diagnostics.FailureLevel(ctx, err); report {
			err = diagnostics.ReportError(ctx, s.diagnosticReporter, diagnostics.Record{
				Component: "control.server",
				Event:     "request_failed",
				Level:     level,
				Err:       err,
			})
		}
	}
	writeControlError(writer, err)
}

func (s *Server) writeRequestRejection(ctx context.Context, writer http.ResponseWriter, summary string, cause error) {
	failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInvalidArgument, Message: summary}, cause)
	failure = diagnostics.ReportError(ctx, s.diagnosticReporter, diagnostics.Record{Component: "control.server", Event: "request_rejected", Level: slog.LevelInfo, Err: failure})
	writeControlError(writer, failure)
}
