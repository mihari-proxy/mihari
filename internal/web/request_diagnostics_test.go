package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGateway_RecordsAuthenticationRejections(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"API without session", "GET", "/version", "", http.StatusUnauthorized},
		{"invalid login form", "POST", AuthPath, "password=%zz", http.StatusBadRequest},
		{"wrong password", "POST", AuthPath, "password=wrong", http.StatusUnauthorized},
		{"login method", "DELETE", AuthPath, "", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reporter, out := newWebDiagnostics()
			gateway, err := New(Options{Addr: "127.0.0.1:0", Auth: Authenticator{WebCredential: task5WebCredential}, ControllerURL: "http://private-address", Reporter: reporter})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			gateway.handler().ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d", response.Code, tc.status)
			}
			assertWebDiagnostics(t, out, "request.rejected", "INFO", 1)
			if tc.name == "invalid login form" && !strings.Contains(out.text(), "%zz") {
				t.Fatal("form parser cause lost")
			}
		})
	}
}
