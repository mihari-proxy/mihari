package client

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstallationClient_PreservesUnknownAndUsesReadOnlyRoute(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/install/status" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("incorrect authenticated status request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema":"mihari.install-status/v1","kind":"unknown","service_state":"unknown","start_failed":false,"reason":"record_invalid","id":""}`))
	}))
	t.Cleanup(s.Close)
	got, err := NewHTTP(s.URL, "token", s.Client()).GetInstallationStatus(context.Background())
	if err != nil || got.Kind != "unknown" || calls != 1 {
		t.Fatalf("kind=%q calls=%d err=%v", got.Kind, calls, err)
	}
}

func TestInstallationClient_RejectsOversizedObservation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", 4096) + `{"schema":"mihari.install-status/v1","kind":"unknown","service_state":"unknown","start_failed":false,"reason":"record_invalid","id":""}`))
	}))
	t.Cleanup(s.Close)
	_, err := NewHTTP(s.URL, "token", s.Client()).GetInstallationStatus(context.Background())
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure {
		t.Fatalf("oversized status err=%v", err)
	}
}
