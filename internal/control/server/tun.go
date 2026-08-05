package server

import (
	"net/http"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	runtimeapi "github.com/LeeShunEE/mihari/internal/runtime"
)

func (s *Server) tunRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/tun", s.tunStatus)
	mux.HandleFunc("POST /v1/tun/enable", s.enableTun)
	mux.HandleFunc("POST /v1/tun/disable", s.disableTun)
}

func (s *Server) tunStatus(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	status, err := s.runtime.TunStatus(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) enableTun(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.TunMutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	status, err := s.runtime.EnableTun(request.Context(), runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	})
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) disableTun(writer http.ResponseWriter, request *http.Request) {
	if !s.requireRuntime(writer) {
		return
	}
	var body protocol.TunMutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	status, err := s.runtime.DisableTun(request.Context(), runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	})
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}
