package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

type configOverrideRuntime struct {
	fakeRuntime
	category string
}

func (f *configOverrideRuntime) ConfigOverrides(context.Context) (protocol.ConfigOverridesStatus, error) {
	return protocol.ConfigOverridesStatus{Revision: 8}, nil
}
func (f *configOverrideRuntime) PreviewConfigOverrides(context.Context, protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error) {
	return protocol.ConfigOverridesPreview{Revision: 8}, nil
}
func (f *configOverrideRuntime) UpdateConfigOverrides(_ context.Context, _ runtimeapi.Operation, category string, _ protocol.ConfigOverridesUpdateRequest) (protocol.ConfigOverridesStatus, error) {
	f.category = category
	return protocol.ConfigOverridesStatus{Revision: 9}, nil
}

func TestConfigOverridesLocalRoutes(t *testing.T) {
	f := &configOverrideRuntime{}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f})
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/config-overrides", "", 200},
		{"PUT", "/v1/config-overrides/dns", `{"operation_id":"edit","if_revision":8,"dns":{}}`, 200},
		{"PUT", "/v1/config-overrides/fragments", `{"operation_id":"edit","if_revision":8,"fragments":[]}`, 200},
		{"PUT", "/v1/config-overrides/dns", `{"operation_id":"edit","fragments":[]}`, 400},
		{"PUT", "/v1/config-overrides/dns", `{"operation_id":"missing-draft"}`, 400},
		{"PUT", "/v1/config-overrides/fragments", `{"operation_id":"missing-draft","fragments":null}`, 400},
		{"POST", "/v1/config-overrides/preview", `{"category":"dns"}`, 200},
	} {
		req := authorizedRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s %s = %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if f.category != "fragments" {
		t.Fatalf("category=%q", f.category)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/config-overrides", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
}
