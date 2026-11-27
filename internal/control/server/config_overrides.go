package server

import (
	"context"
	"net/http"
	"strconv"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

type configOverridesAPI interface {
	ConfigOverrides(context.Context) (protocol.ConfigOverridesStatus, error)
	UpdateConfigOverrides(context.Context, runtimeapi.Operation, string, protocol.ConfigOverridesUpdateRequest) (protocol.ConfigOverridesStatus, error)
	PreviewConfigOverrides(context.Context, protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error)
}

func (s *Server) configOverridesRoutes(mux *http.ServeMux) {
	api, ok := s.runtime.(configOverridesAPI)
	unavailable := func(w http.ResponseWriter, r *http.Request) bool {
		if ok {
			return false
		}
		s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "configuration overrides unavailable"})
		return true
	}
	mux.HandleFunc("GET /v1/config-overrides", func(w http.ResponseWriter, r *http.Request) {
		if unavailable(w, r) {
			return
		}
		v, err := api.ConfigOverrides(r.Context())
		if err != nil {
			s.writeControlError(r.Context(), w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	for _, category := range []string{"dns", "fragments"} {
		mux.HandleFunc("PUT /v1/config-overrides/"+category, func(w http.ResponseWriter, r *http.Request) {
			if unavailable(w, r) {
				return
			}
			var req protocol.ConfigOverridesUpdateRequest
			if !s.decodeControlJSON(w, r, &req) || !s.requireOperationID(r.Context(), w, req.OperationID) {
				return
			}
			if (category == "dns" && (req.Fragments != nil || req.DNS == nil)) || (category == "fragments" && (req.DNS != nil || req.Fragments == nil)) {
				s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "send the complete collection for the selected override category"})
				return
			}
			v, err := api.UpdateConfigOverrides(r.Context(), runtimeapi.Operation{ID: req.OperationID, IfRevision: req.IfRevision, Source: "control"}, category, req)
			if err != nil {
				s.writeControlError(r.Context(), w, err)
				return
			}
			writeJSON(w, http.StatusOK, v)
		})
	}
	mux.HandleFunc("POST /v1/config-overrides/preview", func(w http.ResponseWriter, r *http.Request) {
		if unavailable(w, r) {
			return
		}
		var req protocol.ConfigOverridesPreviewRequest
		if !s.decodeControlJSON(w, r, &req) {
			return
		}
		query := r.URL.Query()
		if query.Has("snapshot") || query.Has("offset") {
			snapshot := query.Get("snapshot")
			offset, err := strconv.Atoi(query.Get("offset"))
			if len(query["snapshot"]) != 1 || len(query["offset"]) != 1 || err != nil || offset < 0 || snapshot == "" ||
				(req.Snapshot != "" && req.Snapshot != snapshot) || (req.Offset != 0 && req.Offset != offset) {
				s.writeRequestRejection(r.Context(), w, "invalid preview continuation", nil)
				return
			}
			req.Offset, req.Snapshot = offset, snapshot
		}
		v, err := api.PreviewConfigOverrides(r.Context(), req)
		if err != nil {
			s.writeControlError(r.Context(), w, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
}
