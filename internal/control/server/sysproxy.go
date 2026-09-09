package server

import (
	"github.com/mihari-proxy/mihari/internal/logging"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

func (s *Server) systemProxyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/system-proxy", s.systemProxyStatus)
	mux.HandleFunc("POST /v1/system-proxy/enable", s.enableSystemProxy)
	mux.HandleFunc("POST /v1/system-proxy/disable", s.disableSystemProxy)
}

func (s *Server) systemProxyStatus(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(request.Context(), writer) {
		return
	}
	status, err := s.runtime.SystemProxyStatus(request.Context())
	if err != nil {
		s.writeControlError(request.Context(), writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) enableSystemProxy(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(request.Context(), writer) {
		return
	}
	var body protocol.SystemProxyMutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "system_proxy.enable"})
	status, err := s.runtime.EnableSystemProxy(ctx, runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	}, body.Force)
	if err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) disableSystemProxy(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(request.Context(), writer) {
		return
	}
	var body protocol.SystemProxyMutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	// Force is intentionally ignored on disable; foreign proxies are refused by runtime.
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "system_proxy.disable"})
	status, err := s.runtime.DisableSystemProxy(ctx, runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	})
	if err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}
