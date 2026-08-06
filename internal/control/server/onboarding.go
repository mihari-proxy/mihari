package server

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type onboardingAPI interface {
	OnboardingStatus(context.Context) (onboarding.Snapshot, error)
	UpdateOnboarding(context.Context, runtimeapi.Operation, onboarding.Update) (onboarding.Snapshot, error)
}

func (s *Server) onboardingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/onboarding", s.onboardingStatus)
	mux.HandleFunc("PATCH /v1/onboarding", s.updateOnboarding)
}

func (s *Server) onboardingStatus(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime.(onboardingAPI)
	if !ok {
		writeControlError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "onboarding service is unavailable"})
		return
	}
	status, err := runtime.OnboardingStatus(request.Context())
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, onboardingDTO(status))
}

func (s *Server) updateOnboarding(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime.(onboardingAPI)
	if !ok {
		writeControlError(writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "onboarding service is unavailable"})
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
	status, err := runtime.UpdateOnboarding(request.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, onboarding.Update{Complete: body.Complete, MixedAddr: body.MixedAddr, ControllerAddr: body.ControllerAddr, WebAddr: body.WebAddr})
	if err != nil {
		writeControlError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, onboardingDTO(status))
}

func onboardingDTO(snapshot onboarding.Snapshot) protocol.OnboardingStatus {
	status := snapshot.Status
	return protocol.OnboardingStatus{Schema: "mihari/v1", Revision: snapshot.Revision, Complete: status.Complete, MixedAddr: status.MixedAddr, ControllerAddr: status.ControllerAddr, WebAddr: status.WebAddr, RestartRequired: status.RestartRequired}
}
