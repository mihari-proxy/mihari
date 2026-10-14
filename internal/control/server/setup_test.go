package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestSetupMode_OnlyAcceptedBySetupOperations(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		accepted     bool
	}{
		{http.MethodPost, "/v1/core/install", true},
		{http.MethodPost, "/v1/core/restart", false},
		{http.MethodPost, "/v1/core/reinstall", false},
		{http.MethodPost, "/v1/geoip/update", true},
		{http.MethodPost, "/v1/subscriptions", true},
		{http.MethodPost, "/v1/subscriptions/a/refresh", true},
		{http.MethodPut, "/v1/subscriptions/a/active", false},
		{http.MethodDelete, "/v1/subscriptions/a", false},
		{http.MethodDelete, "/v1/connections", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			f := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}}
			s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f})
			w := httptest.NewRecorder()
			body := `{"operation_id":"op","execution_mode":"setup"}`
			if tc.path == "/v1/subscriptions" {
				body = `{"operation_id":"op","execution_mode":"setup","name":"A","url":"https://fixture.test/a"}`
			}
			s.Handler().ServeHTTP(w, authorizedRequest(tc.method, tc.path, bytes.NewBufferString(body)))
			if tc.accepted {
				if w.Code < 200 || w.Code >= 300 {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				op := f.fakeRuntime.operation
				if tc.path == "/v1/subscriptions" || tc.path == "/v1/subscriptions/a/refresh" {
					op = f.operation
				}
				if op.ExecutionMode != protocol.ExecutionModeSetup {
					t.Fatalf("mode lost: %+v", op)
				}
			} else if w.Code != http.StatusBadRequest {
				t.Fatalf("unsupported mode accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

type setupRecoverySurface struct{ *fakeOnboardingRuntime }

func (*setupRecoverySurface) SupportsSetupTarget() bool { return true }

func TestSetupMode_RestrictedRecoveryAdvertisesOnlyAvailableResources(t *testing.T) {
	f := &setupRecoverySurface{&fakeOnboardingRuntime{fakeRuntime: &fakeRuntime{}}}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{Health: "degraded"}), Onboarding: f})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/v1/status", nil))
	var status protocol.Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(status.Capabilities, protocol.CapabilitySetupTarget) || slices.Contains(status.Capabilities, protocol.CapabilityCore) {
		t.Fatalf("capabilities=%v", status.Capabilities)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodPatch, "/v1/onboarding", bytes.NewBufferString(`{"operation_id":"ports","execution_mode":"setup","web_addr":"127.0.0.1:19291"}`)))
	if w.Code != http.StatusOK || f.operation.ExecutionMode != protocol.ExecutionModeSetup {
		t.Fatalf("status=%d op=%+v", w.Code, f.operation)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/core/install", bytes.NewBufferString(`{"operation_id":"forbidden","execution_mode":"setup"}`)))
	if w.Code != http.StatusConflict {
		t.Fatalf("recovery opened core: %d", w.Code)
	}
}

func TestSetupMode_UnknownModeRejectedBeforeRuntime(t *testing.T) {
	f := &fakeRuntime{}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/core/install", bytes.NewBufferString(`{"operation_id":"op","execution_mode":"force-everything"}`)))
	if w.Code != http.StatusBadRequest || f.operation.ID != "" {
		t.Fatalf("status=%d operation=%+v", w.Code, f.operation)
	}
}
