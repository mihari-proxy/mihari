package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// Request validation owns expected rejections before an execution identity
// exists. Raw decoder causes remain internal to the file diagnostic.
func (s *Server) reportRequestRejection(ctx context.Context, err error) {
	if s.diagnosticReporter != nil {
		s.diagnosticReporter(ctx, diagnostics.Record{Component: "control.server", Event: "request_rejected", Level: slog.LevelInfo, Err: err})
	}
}

func (s *Server) writeInvalidArgument(ctx context.Context, writer http.ResponseWriter, message string) {
	s.reportRequestRejection(ctx, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: message})
	writeInvalidArgument(writer, message)
}

func (s *Server) writeControlError(ctx context.Context, writer http.ResponseWriter, err error) {
	if s.diagnosticReporter != nil && !diagnostics.AlreadyReported(err) {
		if level, report := diagnostics.FailureLevel(ctx, err); report {
			s.diagnosticReporter(ctx, diagnostics.Record{
				Component: "control.server",
				Event:     "request_failed",
				Level:     level,
				Err:       err,
			})
		}
	}
	writeControlError(writer, err)
}
