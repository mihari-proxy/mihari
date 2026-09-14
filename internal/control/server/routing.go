package server

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"net/http"
)

type routingAPI interface {
	RoutingStatus(context.Context) (protocol.RoutingStatus, error)
	UpdateRouting(context.Context, runtimeapi.Operation, string) (protocol.RoutingStatus, error)
}

func (s *Server) routingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/routing", s.routingStatus)
	mux.HandleFunc("PATCH /v1/routing", s.updateRouting)
}

func (s *Server) routingRuntime(ctx context.Context, w http.ResponseWriter) (routingAPI, bool) {
	r, ok := s.runtime.(routingAPI)
	if !ok {
		s.writeControlError(ctx, w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "routing mode capability is unavailable"})
	}
	return r, ok
}
func (s *Server) routingStatus(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.routingRuntime(r.Context(), w)
	if !ok {
		return
	}
	status, err := runtime.RoutingStatus(r.Context())
	if err != nil {
		s.writeControlError(r.Context(), w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
func (s *Server) updateRouting(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.routingRuntime(r.Context(), w)
	if !ok {
		return
	}
	var body protocol.RoutingUpdateRequest
	if !s.decodeControlJSON(w, r, &body) || !s.requireOperationID(r.Context(), w, body.OperationID) {
		return
	}
	if !protocol.ValidRoutingMode(body.Mode) {
		s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid routing mode"})
		return
	}
	ctx := logging.WithOperation(r.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "routing.update"})
	status, err := runtime.UpdateRouting(ctx, runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, body.Mode)
	if err != nil {
		s.writeControlError(ctx, w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
