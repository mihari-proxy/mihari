package server

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
	"strings"
)

func (s *Server) validateExecutionMode(w http.ResponseWriter, r *http.Request, body any) bool {
	var mode string
	switch v := body.(type) {
	case *protocol.MutationRequest:
		mode = v.ExecutionMode
	case *protocol.SubscriptionAddRequest:
		mode = v.ExecutionMode
	case *protocol.OnboardingUpdateRequest:
		mode = v.ExecutionMode
	}
	if mode == "" {
		return true
	}
	allowed := r.Method == http.MethodPatch && r.URL.Path == "/v1/onboarding"
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/v1/core/install", "/v1/geoip/update", "/v1/subscriptions":
			allowed = true
		default:
			path := strings.TrimPrefix(r.URL.Path, "/v1/subscriptions/")
			allowed = path != r.URL.Path && strings.HasSuffix(path, "/refresh") && strings.Count(path, "/") == 1
		}
	}
	if mode != protocol.ExecutionModeSetup || !allowed {
		s.writeInvalidArgument(r.Context(), w, "execution_mode is not supported for this operation")
		return false
	}
	return true
}
