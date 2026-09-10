package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

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
