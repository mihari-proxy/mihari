package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// serviceStatusAPI is the optional OS service-status contract. Runtimes that
// implement it let GET /v1/service/status feed the onboarding review summary;
// runtimes without it get a 409 (mirrors onboardingAPI, design §6.2).
type serviceStatusAPI interface {
	ServiceStatus(context.Context) (protocol.ServiceStatus, error)
}

func (s *Server) serviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/service/status", s.reportServiceStatus)
}

func (s *Server) reportServiceStatus(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime.(serviceStatusAPI)
	if !ok {
		s.writeControlError(request.Context(), writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "service status is unavailable"})
		return
	}
	status, err := runtime.ServiceStatus(request.Context())
	if err != nil {
		s.writeControlError(request.Context(), writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}
