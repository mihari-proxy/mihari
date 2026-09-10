package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/panel"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type panelAPI interface {
	WebGUIStatus(context.Context) (protocol.WebGUIStatus, error)
	ListPanels(context.Context) ([]panel.PanelInfo, error)
	InstallPanel(context.Context, runtimeapi.Operation, string, string) error
	UpdatePanel(context.Context, runtimeapi.Operation, string) error
	ActivatePanel(context.Context, runtimeapi.Operation, string) error
	RollbackPanel(context.Context, runtimeapi.Operation, string) error
	UninstallPanel(context.Context, runtimeapi.Operation, string) error
	ReinstallPanel(context.Context, runtimeapi.Operation, string) error
	OpenWebGUI(context.Context, string) (string, string, error)
}

func (s *Server) webGUIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/web-gui", s.webGUIStatus)
	mux.HandleFunc("POST /v1/web-gui/open", s.openWebGUI)
	mux.HandleFunc("GET /v1/panels", s.listPanels)
	mux.HandleFunc("POST /v1/panels/{id}/install", s.installPanel)
	mux.HandleFunc("POST /v1/panels/{id}/update", s.updatePanel)
	mux.HandleFunc("PUT /v1/panels/{id}/active", s.activatePanel)
	mux.HandleFunc("POST /v1/panels/{id}/rollback", s.rollbackPanel)
	mux.HandleFunc("POST /v1/panels/{id}/uninstall", s.uninstallPanel)
	mux.HandleFunc("POST /v1/panels/{id}/reinstall", s.reinstallPanel)
}

func (s *Server) panelRuntime(ctx context.Context, writer http.ResponseWriter) (panelAPI, bool) {
	runtime, ok := s.runtime.(panelAPI)
	if !ok {
		s.writeControlError(ctx, writer, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"})
	}
	return runtime, ok
}

func (s *Server) webGUIStatus(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.panelRuntime(request.Context(), writer)
	if !ok {
		return
	}
	status, err := runtime.WebGUIStatus(request.Context())
	if err != nil {
		s.writeControlError(request.Context(), writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) openWebGUI(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.panelRuntime(request.Context(), writer)
	if !ok {
		return
	}
	var body protocol.WebGUIOpenRequest
	// Empty body is allowed and opens the default active panel.
	if request.ContentLength != 0 {
		if !decodeControlJSON(writer, request, &body) {
			return
		}
	}
	openURL, panelID, err := runtime.OpenWebGUI(request.Context(), body.Panel)
	if err != nil {
		s.writeControlError(request.Context(), writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.WebGUIOpenResult{Schema: "mihari/v1", OpenURL: openURL, Panel: panelID})
}

func (s *Server) listPanels(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.panelRuntime(request.Context(), writer)
	if !ok {
		return
	}
	panels, err := runtime.ListPanels(request.Context())
	if err != nil {
		s.writeControlError(request.Context(), writer, err)
		return
	}
	dto := make([]protocol.PanelStatus, 0, len(panels))
	for _, info := range panels {
		dto = append(dto, protocol.PanelStatus{
			ID: info.ID, Name: info.Name, Active: info.Active,
			InstalledBuild: info.InstalledBuild, LatestBuild: info.LatestBuild,
			Health: info.Health, RollbackBuild: info.RollbackBuild,
		})
	}
	writeJSON(writer, http.StatusOK, protocol.PanelList{
		Schema: "mihari/v1", Revision: s.runtime.Snapshot().Revision, Panels: dto,
	})
}

func (s *Server) installPanel(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.panelRuntime(request.Context(), writer)
	if !ok {
		return
	}
	var body protocol.PanelInstallRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	id := request.PathValue("id")
	if id == "" {
		writeInvalidArgument(writer, "panel id is required")
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: "panel.install"})
	if err := runtime.InstallPanel(ctx, runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	}, id, body.Build); err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.MutationResult{
		Schema: "mihari/v1", OperationID: body.OperationID, Revision: s.runtime.Snapshot().Revision,
	})
}

func (s *Server) updatePanel(writer http.ResponseWriter, request *http.Request) {
	s.panelMutation(writer, request, "panel.update", func(ctx context.Context, runtime panelAPI, operation runtimeapi.Operation, id string) error {
		return runtime.UpdatePanel(ctx, operation, id)
	})
}

func (s *Server) activatePanel(writer http.ResponseWriter, request *http.Request) {
	s.panelMutation(writer, request, "panel.activate", func(ctx context.Context, runtime panelAPI, operation runtimeapi.Operation, id string) error {
		return runtime.ActivatePanel(ctx, operation, id)
	})
}

func (s *Server) rollbackPanel(writer http.ResponseWriter, request *http.Request) {
	s.panelMutation(writer, request, "panel.rollback", func(ctx context.Context, runtime panelAPI, operation runtimeapi.Operation, id string) error {
		return runtime.RollbackPanel(ctx, operation, id)
	})
}

func (s *Server) uninstallPanel(writer http.ResponseWriter, request *http.Request) {
	s.panelMutation(writer, request, "panel.uninstall", func(ctx context.Context, runtime panelAPI, operation runtimeapi.Operation, id string) error {
		return runtime.UninstallPanel(ctx, operation, id)
	})
}

func (s *Server) reinstallPanel(writer http.ResponseWriter, request *http.Request) {
	s.panelMutation(writer, request, "panel.reinstall", func(ctx context.Context, runtime panelAPI, operation runtimeapi.Operation, id string) error {
		return runtime.ReinstallPanel(ctx, operation, id)
	})
}

func (s *Server) panelMutation(writer http.ResponseWriter, request *http.Request, name string, mutate func(context.Context, panelAPI, runtimeapi.Operation, string) error) {
	runtime, ok := s.panelRuntime(request.Context(), writer)
	if !ok {
		return
	}
	var body protocol.MutationRequest
	if !decodeControlJSON(writer, request, &body) || !requireOperationID(writer, body.OperationID) {
		return
	}
	id := strings.TrimSpace(request.PathValue("id"))
	if id == "" {
		writeInvalidArgument(writer, "panel id is required")
		return
	}
	ctx := logging.WithOperation(request.Context(), logging.OperationMetadata{ID: body.OperationID, Name: name})
	if err := mutate(ctx, runtime, runtimeapi.Operation{
		ID: body.OperationID, Source: "control", IfRevision: body.IfRevision,
	}, id); err != nil {
		s.writeControlError(ctx, writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, protocol.MutationResult{
		Schema: "mihari/v1", OperationID: body.OperationID, Revision: s.runtime.Snapshot().Revision,
	})
}
