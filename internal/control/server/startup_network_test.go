package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

type startupNetworkRuntime struct {
	fakeRuntime
	startup *protocol.StartupNetworkStatus
}

func (r *startupNetworkRuntime) StartupNetworkStatus() *protocol.StartupNetworkStatus {
	return r.startup
}

func TestStatus_StartupNetworkIsOptionalObservation(t *testing.T) {
	runtime := &startupNetworkRuntime{}
	s := New(Options{Token: "test-token", Store: state.NewStore(state.Snapshot{Revision: 7}), Runtime: runtime})
	for _, applying := range []*protocol.StartupNetworkStatus{nil, {SystemProxyApplying: true}, {TunApplying: true}, nil} {
		runtime.startup = applying
		req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		var got struct {
			Revision uint64          `json:"revision"`
			Startup  map[string]bool `json:"startup_network"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || got.Revision != 7 {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if applying == nil {
			if got.Startup != nil {
				t.Fatalf("idle field not omitted: %s", response.Body.String())
			}
		} else if len(got.Startup) != 2 || got.Startup["system_proxy_applying"] != applying.SystemProxyApplying || got.Startup["tun_applying"] != applying.TunApplying {
			t.Fatalf("startup=%v want=%+v", got.Startup, applying)
		}
	}
}
