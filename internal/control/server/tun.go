package server

import (
	"github.com/mihari-proxy/mihari/internal/logging"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

func (s *Server) tunRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/tun", s.tunStatus)
	mux.HandleFunc("POST /v1/tun/enable", s.enableTun)
	mux.HandleFunc("POST /v1/tun/disable", s.disableTun)
}

func (s *Server) tunStatus(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(request.Context(), writer) {
		return
	}
	status, err := s.runtime.TunStatus(request.Context())
	if err != nil {
		s.writeControlError(request.Context(), writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) enableTun(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(request.Context(), writer) {
		return
	}
	var body protocol.TunMutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "tun.enable"})
	status, err := s.runtime.EnableTun(ctx, runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	}, body.Force)
	if err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) disableTun(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(request.Context(), writer) {
		return
	}
	var body protocol.TunMutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "tun.disable"})
	status, err := s.runtime.DisableTun(ctx, runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	})
	if err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}
