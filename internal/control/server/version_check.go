package server

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
)

func (s *Server) checkCoreVersion(w http.ResponseWriter, r *http.Request) {
	checker, ok := s.runtime.(interface {
		CheckCoreVersion(context.Context) (protocol.VersionCheck, error)
	})
	if !ok {
		s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core version check is unavailable"})
		return
	}
	result, err := checker.CheckCoreVersion(r.Context())
	if err != nil {
		s.writeControlError(r.Context(), w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) checkPanelVersion(w http.ResponseWriter, r *http.Request) {
	checker, ok := s.runtime.(interface {
		CheckPanelVersion(context.Context, string) (protocol.VersionCheck, error)
	})
	if !ok {
		s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel version check is unavailable"})
		return
	}
	result, err := checker.CheckPanelVersion(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeControlError(r.Context(), w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
