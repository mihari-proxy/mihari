package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/preferences"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

type preferencesAPI interface {
	Snapshot() state.Snapshot
	TUIPreferences() preferences.Preferences
	UpdateTUIPreferences(context.Context, runtimeapi.Operation, preferences.Update) (preferences.Preferences, error)
}

func (s *Server) preferencesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/preferences/tui", s.getTUIPreferences)
	mux.HandleFunc("PATCH /v1/preferences/tui", s.updateTUIPreferences)
}

func (s *Server) preferencesRuntime(writer http.ResponseWriter) (preferencesAPI, bool) {
	runtime, ok := s.runtime.(preferencesAPI)
	if !ok {
		writeControlError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "TUI preferences are unavailable"})
	}
	return runtime, ok
}

func (s *Server) getTUIPreferences(writer http.ResponseWriter, _ *http.Request) {
	runtime, ok := s.preferencesRuntime(writer)
	if !ok {
		return
	}
	writeJSON(writer, http.StatusOK, tuiPreferencesDTO(runtime.TUIPreferences(), runtime.Snapshot().Revision))
}

func (s *Server) updateTUIPreferences(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.preferencesRuntime(writer)
	if !ok {
		return
	}
	var body protocol.UpdateTUIPreferencesRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	updated, err := runtime.UpdateTUIPreferences(request.Context(), runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	}, preferences.Update{ConnectionsColumns: body.ConnectionsColumns})
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, tuiPreferencesDTO(updated, runtime.Snapshot().Revision))
}

func tuiPreferencesDTO(value preferences.Preferences, revision uint64) protocol.TUIPreferences {
	return protocol.TUIPreferences{
		Schema: "mihari/v1", Revision: revision,
		ConnectionsColumns: append([]string(nil), value.ConnectionsColumns...),
	}
}
