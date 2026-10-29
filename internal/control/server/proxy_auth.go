package server

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"net/http"
)

type proxyAuthAPI interface {
	ProxyAuth(context.Context, bool) (protocol.ProxyAuthStatus, error)
	UpdateProxyAuth(context.Context, runtimeapi.Operation, protocol.ProxyAuthUpdateRequest) (protocol.ProxyAuthStatus, error)
}

func (s *Server) proxyAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/proxy-auth", func(w http.ResponseWriter, r *http.Request) {
		api, ok := s.runtime.(proxyAuthAPI)
		if !ok {
			s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "proxy authentication capability unavailable"})
			return
		}
		value, err := api.ProxyAuth(r.Context(), r.URL.Query().Get("reveal") == "true")
		if err != nil {
			s.writeControlError(r.Context(), w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	})
	mux.HandleFunc("PUT /v1/proxy-auth", func(w http.ResponseWriter, r *http.Request) {
		api, ok := s.runtime.(proxyAuthAPI)
		if !ok {
			s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "proxy authentication capability unavailable"})
			return
		}
		var body protocol.ProxyAuthUpdateRequest
		if !s.decodeControlJSON(w, r, &body) || !s.requireOperationID(r.Context(), w, body.OperationID) {
			return
		}
		value, err := api.UpdateProxyAuth(r.Context(), runtimeapi.Operation{ID: body.OperationID, Source: "control", IfRevision: body.IfRevision}, body)
		if err != nil {
			s.writeControlError(r.Context(), w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	})
}
