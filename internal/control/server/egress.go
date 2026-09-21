package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type egressAPI interface {
	EgressStatus(context.Context) (protocol.EgressStatus, error)
	UpdateEgress(context.Context, runtimeapi.Operation, protocol.EgressSelection) (protocol.EgressStatus, error)
}

func (s *Server) egressRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/egress", s.egressStatus)
	mux.HandleFunc("PATCH /v1/egress", s.updateEgress)
}

func (s *Server) egressRuntime(ctx context.Context, w http.ResponseWriter) (egressAPI, bool) {
	r, ok := s.runtime.(egressAPI)
	if !ok {
		s.writeControlError(ctx, w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "outbound interface capability is unavailable"})
	}
	return r, ok
}

func (s *Server) egressStatus(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.egressRuntime(r.Context(), w)
	if !ok {
		return
	}
	status, err := runtime.EgressStatus(r.Context())
	if err != nil {
		s.writeControlError(r.Context(), w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) updateEgress(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.egressRuntime(r.Context(), w)
	if !ok {
		return
	}
	var body protocol.EgressUpdateRequest
	if !s.decodeControlJSON(w, r, &body) || !s.requireOperationID(r.Context(), w, body.OperationID) {
		return
	}
	if !protocol.ValidEgressSelection(body.Mode, body.InterfaceName) {
		s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid outbound interface selection"})
		return
	}
	ctx := logging.WithOperation(r.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "egress.update"})
	status, err := runtime.UpdateEgress(ctx, runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, protocol.EgressSelection{Mode: body.Mode, InterfaceName: body.InterfaceName})
	if err != nil {
		s.writeControlError(ctx, w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
