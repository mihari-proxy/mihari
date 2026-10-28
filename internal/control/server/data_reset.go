package server

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"net/http"
)

type dataResetAPI interface {
	DataResetPreview(context.Context) (protocol.ResetPreview, error)
	ResetBusinessData(context.Context, runtimeapi.Operation, protocol.DataResetRequest) (protocol.DataResetResult, error)
}

func (s *Server) dataResetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/data-reset", func(w http.ResponseWriter, r *http.Request) {
		api, ok := s.runtime.(dataResetAPI)
		if !ok {
			s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "business reset is unavailable"})
			return
		}
		v, err := api.DataResetPreview(r.Context())
		if err != nil {
			s.writeControlError(r.Context(), w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	mux.HandleFunc("POST /v1/data-reset", func(w http.ResponseWriter, r *http.Request) {
		api, ok := s.runtime.(dataResetAPI)
		if !ok {
			s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "business reset is unavailable"})
			return
		}
		var body protocol.DataResetRequest
		if !s.decodeControlJSON(w, r, &body) || !s.requireOperationID(r.Context(), w, body.OperationID) {
			return
		}
		v, err := api.ResetBusinessData(r.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, body)
		if err != nil {
			s.writeControlError(r.Context(), w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
}
