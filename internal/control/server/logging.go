package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type loggingAPI interface {
	LoggingStatus(context.Context) (protocol.LoggingStatus, error)
	UpdateLogging(context.Context, runtimeapi.Operation, runtimeapi.LoggingUpdate) (protocol.LoggingStatus, error)
}

func (s *Server) loggingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/logging", s.loggingStatus)
	mux.HandleFunc("PATCH /v1/logging", s.updateLogging)
	mux.HandleFunc("POST /v1/logging/snapshot", s.loggingSnapshot)
}

func (s *Server) loggingRuntime(ctx context.Context, writer http.ResponseWriter) (loggingAPI, bool) {
	runtime, ok := s.runtime.(loggingAPI)
	if !ok {
		s.writeControlError(ctx, writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "logging runtime is unavailable"})
	}
	return runtime, ok
}

func (s *Server) loggingStatus(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.loggingRuntime(request.Context(), writer)
	if !ok {
		return
	}
	status, err := runtime.LoggingStatus(request.Context())
	if err != nil {
		s.writeControlError(request.Context(), writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) updateLogging(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.loggingRuntime(request.Context(), writer)
	if !ok {
		return
	}
	var body protocol.LoggingUpdateRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "logging.update"})
	if err := validateLoggingUpdate(body); err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	status, err := runtime.UpdateLogging(ctx, runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	}, runtimeapi.LoggingUpdate{Level: body.Level, MaxSizeMB: body.MaxSizeMB, MaxFiles: body.MaxFiles})
	if err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func validateLoggingUpdate(request protocol.LoggingUpdateRequest) error {
	if request.Level == nil && request.MaxSizeMB == nil && request.MaxFiles == nil {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "logging update is empty"}
	}
	if request.Level != nil && !validLoggingLevel(*request.Level) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging level"}
	}
	if request.MaxSizeMB != nil && (*request.MaxSizeMB < 1 || *request.MaxSizeMB > 100) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging max size"}
	}
	if request.MaxFiles != nil && (*request.MaxFiles < 1 || *request.MaxFiles > 10) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging max files"}
	}
	return nil
}

func validLoggingLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
