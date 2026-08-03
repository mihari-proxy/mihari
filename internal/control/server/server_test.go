package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/state"
)

func TestStatusRequiresExactBearerToken(t *testing.T) {
	for _, authorization := range []string{"", "Bearer test", "Bearer test-token-extra"} {
		t.Run(authorization, func(t *testing.T) {
			server := newTestServer()
			request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
			request.Header.Set("Authorization", authorization)
			response := httptest.NewRecorder()

			server.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d", response.Code)
			}
			var envelope protocol.ErrorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != protocol.CodePermissionDenied {
				t.Fatalf("code=%q", envelope.Error.Code)
			}
		})
	}
}

func TestStatusReturnsStableEnvelope(t *testing.T) {
	server := newTestServer()
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got protocol.Status
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "mihari/v1" || got.ProtocolVersion != "v1" || got.Revision != 7 || got.Health != "ok" {
		t.Fatalf("status=%#v", got)
	}
}

func TestAuthenticatedUnknownRouteReturnsNotFound(t *testing.T) {
	server := newTestServer()
	request := httptest.NewRequest(http.MethodGet, "/v1/unknown", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d", response.Code)
	}
}

func newTestServer() *Server {
	store := state.NewStore(state.Snapshot{
		Revision:  7,
		Version:   "dev",
		StartedAt: time.Unix(100, 0).UTC(),
		Health:    "ok",
	})
	return New(Options{Token: "test-token", Store: store})
}
