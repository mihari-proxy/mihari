package server

import (
	"context"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"net/http"
	"net/http/httptest"
	"testing"
)

type versionRuntime struct {
	*fakeRuntime
	calls int
	id    string
	err   error
}

func (r *versionRuntime) CheckCoreVersion(context.Context) (protocol.VersionCheck, error) {
	r.calls++
	return protocol.VersionCheck{Schema: "mihari/v1", Latest: "alpha-abc1234", Channel: "alpha"}, r.err
}
func (r *versionRuntime) CheckPanelVersion(_ context.Context, id string) (protocol.VersionCheck, error) {
	r.calls++
	r.id = id
	return protocol.VersionCheck{Schema: "mihari/v1", Latest: "abc123456789"}, r.err
}
func TestVersionCheckRoutes_ClientRoundTripAndAuthentication(t *testing.T) {
	runtime := &versionRuntime{fakeRuntime: &fakeRuntime{snapshot: state.Snapshot{Revision: 7}}}
	handler := New(Options{Token: "token", Store: state.NewStore(runtime.snapshot), Runtime: runtime}).Handler()
	server := httptest.NewServer(handler)
	defer server.Close()
	client := controlclient.NewHTTP(server.URL, "token", server.Client())
	core, err := client.CheckCoreVersion(t.Context())
	if err != nil || core.Channel != "alpha" || core.Latest != "alpha-abc1234" || core.Schema != "mihari/v1" {
		t.Fatalf("core=%+v err=%v", core, err)
	}
	panel, err := client.CheckPanelVersion(t.Context(), "metacubexd")
	if err != nil || panel.Latest != "abc123456789" || runtime.id != "metacubexd" {
		t.Fatalf("panel=%+v err=%v id=%s", panel, err, runtime.id)
	}
	for _, path := range []string{"/v1/core/version-check", "/v1/panels/zashboard/version-check"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
	if runtime.calls != 2 || runtime.snapshot.Revision != 7 {
		t.Fatalf("calls=%d revision=%d", runtime.calls, runtime.snapshot.Revision)
	}
	runtime.err = protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "version check failed"}
	if _, err := client.CheckCoreVersion(t.Context()); err == nil {
		t.Fatal("missing core failure")
	}
	if _, err := client.CheckPanelVersion(t.Context(), "zashboard"); err == nil {
		t.Fatal("missing panel failure")
	}
}
