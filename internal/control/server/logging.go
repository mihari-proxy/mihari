package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type loggingAPI interface {
	LoggingStatus(context.Context) (protocol.LoggingStatus, error)
	UpdateLogging(context.Context, runtimeapi.Operation, runtimeapi.LoggingUpdate) (protocol.LoggingStatus, error)
}

func (s *Server) loggingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/logging", s.loggingStatus)
	mux.HandleFunc("PATCH /v1/logging", s.updateLogging)
}

func (s *Server) loggingRuntime(writer http.ResponseWriter) (loggingAPI, bool) {
	runtime, ok := s.runtime.(loggingAPI)
	if !ok {
		writeControlError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "logging runtime is unavailable"})
	}
	return runtime, ok
}

func (s *Server) loggingStatus(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.loggingRuntime(writer)
	if !ok {
		return
	}
	status, err := runtime.LoggingStatus(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) updateLogging(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.loggingRuntime(writer)
	if !ok {
		return
	}
	var body protocol.LoggingUpdateRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	if err := validateLoggingUpdate(body); err != nil {
		writeControlError(writer, err)
		return
	}
	status, err := runtime.UpdateLogging(request.Context(), runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	}, runtimeapi.LoggingUpdate{Level: body.Level, MaxSizeMB: body.MaxSizeMB, MaxFiles: body.MaxFiles})
	if err != nil {
		writeControlError(writer, err)
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
