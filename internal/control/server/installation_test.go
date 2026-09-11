package server

import (
	"context"
	"encoding/json"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
	"net/http/httptest"
	"testing"
)

type installationTestRuntime struct {
	fakeRuntime
	calls int
}

func (r *installationTestRuntime) GetInstallationStatus(context.Context) (protocol.InstallationStatus, error) {
	r.calls++
	return protocol.InstallationStatus{Schema: "mihari.install-status/v1", Kind: "unknown", ServiceState: "unknown", Reason: "record_invalid"}, nil
}

func TestInstallationRoute_ReadOnlyAuthenticatedStatus(t *testing.T) {
	runtime := &installationTestRuntime{}
	s := New(Options{Token: "token", Runtime: runtime})
	for _, tc := range []struct {
		method, token string
		code, calls   int
	}{
		{http.MethodGet, "", http.StatusUnauthorized, 0},
		{http.MethodPost, "Bearer token", http.StatusMethodNotAllowed, 0},
		{http.MethodGet, "Bearer token", http.StatusOK, 1},
	} {
		r := httptest.NewRequest(tc.method, "/v1/install/status", nil)
		r.Header.Set("Authorization", tc.token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.code || runtime.calls != tc.calls {
			t.Fatalf("status=%d calls=%d", w.Code, runtime.calls)
		}
		if w.Code == http.StatusOK {
			var got map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != 6 || string(got["kind"]) != `"unknown"` {
				t.Fatal("status exposed unexpected fields or changed classification")
			}
		}
	}
}
