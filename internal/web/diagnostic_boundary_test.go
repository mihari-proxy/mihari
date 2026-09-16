package web

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGateway_LocalDiagnosticHistoryIsNotExposed(t *testing.T) {
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "private"})
	if err != nil {
		t.Fatal(err)
	}
	owner := diagnostics.NewOwner(history, nil)
	owner.Report(context.Background(), diagnostics.Record{Level: slog.LevelError, Err: errors.New("token=fixture-local-private")})
	gateway, err := New(Options{Addr: "127.0.0.1:0", ControllerURL: "http://fixture.invalid", Transport: task5RoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("local diagnostic path reached controller transport")
		return nil, errors.New("unexpected controller call")
	}), Auth: Authenticator{WebCredential: "fixture-browser"}, Panel: memoryPanel{dir: t.TempDir()}, Reporter: owner.Report})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/diagnostics", "/v1/diagnostics/private:1"} {
		for _, authenticated := range []bool{false, true} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			if authenticated {
				request.Header.Set("Authorization", "Bearer fixture-browser")
			}
			response := httptest.NewRecorder()
			gateway.handler().ServeHTTP(response, request)
			if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "fixture-local-private") || strings.Contains(response.Body.String(), "mihari.diagnostic") {
				t.Fatalf("browser exposed local diagnostics: %d", response.Code)
			}
		}
	}
}
