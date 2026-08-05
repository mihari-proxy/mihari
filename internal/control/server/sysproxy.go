package server

import (
	"net/http"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	runtimeapi "github.com/LeeShunEE/mihari/internal/runtime"
)

func (s *Server) systemProxyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/system-proxy", s.systemProxyStatus)
	mux.HandleFunc("POST /v1/system-proxy/enable", s.enableSystemProxy)
	mux.HandleFunc("POST /v1/system-proxy/disable", s.disableSystemProxy)
}

func (s *Server) systemProxyStatus(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	status, err := s.runtime.SystemProxyStatus(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) enableSystemProxy(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.SystemProxyMutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	status, err := s.runtime.EnableSystemProxy(request.Context(), runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	}, body.Force)
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) disableSystemProxy(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.SystemProxyMutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	// Force is intentionally ignored on disable; foreign proxies are refused by runtime.
	status, err := s.runtime.DisableSystemProxy(request.Context(), runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	})
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}
