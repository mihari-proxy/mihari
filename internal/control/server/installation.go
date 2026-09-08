package server

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
)

type installationStatusAPI interface {
	GetInstallationStatus(context.Context) (protocol.InstallationStatus, error)
}

func (s *Server) installationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/install/status", s.reportInstallationStatus)
}

func (s *Server) reportInstallationStatus(w http.ResponseWriter, r *http.Request) {
	runtime, ok := s.runtime.(installationStatusAPI)
	if !ok {
		writeControlError(w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "installation status is unavailable"})
		return
	}
	status, err := runtime.GetInstallationStatus(r.Context())
	if err != nil {
		writeControlError(w, err)
		return
	}
	if err := status.Validate(); err != nil {
		writeControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
