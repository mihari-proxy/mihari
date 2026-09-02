package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type dataResetAPI interface {
	ResetUserData(context.Context, runtimeapi.Operation) (protocol.DataResetResult, error)
}

func (s *Server) dataRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/data/reset", s.resetUserData)
}

func (s *Server) resetUserData(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime.(dataResetAPI)
	if !ok {
		writeControlError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "data reset is unavailable"})
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	result, err := runtime.ResetUserData(request.Context(), runtimeapi.Operation{
		ID: body.OperationID, Source: mutationSource(body.Source), IfRevision: body.IfRevision,
	})
	if err != nil {
		writeControlError(writer, err)
		return
	}
	if result.Schema == "" {
		result.Schema = "mihari/v1"
	}
	if result.OperationID == "" {
		result.OperationID = body.OperationID
	}
	writeJSON(writer, http.StatusOK, result)
}
