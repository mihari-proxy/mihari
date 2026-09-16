package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayLoggingPatch_Allowlist(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		status             int
	}{
		{"debug", "PATCH", `{"log-level":"debug"}`, http.StatusNoContent},
		{"warning", "PATCH", `{"log-level":"warning"}`, http.StatusNoContent},
		{"silent", "PATCH", `{"log-level":"silent"}`, http.StatusBadRequest},
		{"invalid", "PATCH", `{"log-level":42}`, http.StatusBadRequest},
		{"mixed", "PATCH", `{"log-level":"debug","mode":"rule"}`, http.StatusForbidden},
		{"put", "PUT", `{"log-level":"info"}`, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutator := &recordingMutator{}
			s := &Server{Mutator: mutator}
			w := httptest.NewRecorder()
			s.handleConfigMutation(w, httptest.NewRequest(tc.method, "/configs", strings.NewReader(tc.body)))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if (mutator.lastPatch() != nil) != (tc.status == http.StatusNoContent) {
				t.Fatal("mutation allowlist side effects differ")
			}
		})
	}
}
