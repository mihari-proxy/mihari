package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

// OnboardingAPI is the narrow daemon-owned setup surface, also available during port recovery.
type OnboardingAPI interface {
	OnboardingStatus(context.Context) (onboarding.Snapshot, error)
	UpdateOnboarding(context.Context, runtimeapi.Operation, onboarding.Update) (onboarding.Snapshot, error)
}

type onboardingAPI = OnboardingAPI

type setupRequiredAPI interface {
	SetupRequired(context.Context) (bool, error)
}

func (s *Server) onboardingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/onboarding", s.onboardingStatus)
	mux.HandleFunc("PATCH /v1/onboarding", s.updateOnboarding)
}

// onboardingStatus reads the daemon-owned setup surface, including restricted recovery.
func (s *Server) onboardingStatus(writer http.ResponseWriter, request *http.Request) {
	runtime := s.onboarding
	if runtime == nil {
		s.writeControlError(request.Context(), writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "onboarding service is unavailable"})
		return
	}
	status, err := runtime.OnboardingStatus(request.Context())
	if err != nil {
		s.writeControlError(request.Context(), writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, onboardingDTO(status))
}

// updateOnboarding validates a setup mutation and tracks its settlement through the owner.
func (s *Server) updateOnboarding(writer http.ResponseWriter, request *http.Request) {
	runtime := s.onboarding
	if runtime == nil {
		s.writeControlError(request.Context(), writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "onboarding service is unavailable"})
		return
	}
	var body protocol.OnboardingUpdateRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	if body.Complete == nil && body.MixedAddr == nil && body.ControllerAddr == nil && body.WebAddr == nil {
		writeInvalidArgument(writer, "onboarding update is empty")
		return
	}
	defer s.operations.begin(body.OperationID)()
	status, err := runtime.UpdateOnboarding(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, onboarding.Update{Complete: body.Complete, MixedAddr: body.MixedAddr, ControllerAddr: body.ControllerAddr, WebAddr: body.WebAddr})
	if err != nil {
		s.writeControlError(request.Context(), writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, onboardingDTO(status))
}

func onboardingDTO(snapshot onboarding.Snapshot) protocol.OnboardingStatus {
	status := snapshot.Status
	return protocol.OnboardingStatus{Schema: "mihari/v1", Revision: snapshot.Revision, Complete: status.Complete, MixedAddr: status.MixedAddr, ControllerAddr: status.ControllerAddr, WebAddr: status.WebAddr, RestartRequired: status.RestartRequired}
}
