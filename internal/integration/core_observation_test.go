package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestCoreObservation_ControlPollingDoesNotExecuteCore(t *testing.T) {
	for _, version := range []string{"v1.19.0", ""} {
		t.Run(version, func(t *testing.T) {
			onboardingService, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json")})
			if err != nil {
				t.Fatal(err)
			}
			installer := &observationInstaller{}
			var checks atomic.Int64
			store := state.NewStore(state.Snapshot{Health: "ok", Core: state.CoreState{Version: version, Status: "stopped"}})
			manager := runtimeapi.New(runtimeapi.Options{Store: store, Installer: installer, Onboarding: onboardingService,
				BinaryExists: func() bool { checks.Add(1); return true },
			})
			handler := controlserver.New(controlserver.Options{Token: "synthetic-token", Store: store, Runtime: manager}).Handler()
			for range 30 {
				for _, endpoint := range []string{"/v1/status", "/v1/core"} {
					request := httptest.NewRequest(http.MethodGet, endpoint, nil)
					request.Header.Set("Authorization", "Bearer synthetic-token")
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != http.StatusOK {
						t.Fatalf("%s: status=%d body=%s", endpoint, response.Code, response.Body.String())
					}
					if endpoint == "/v1/core" {
						var status protocol.CoreStatus
						if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
							t.Fatal(err)
						}
						if status.LocalReady != (version != "") || status.LocalVersion != version {
							t.Fatalf("core observation changed: %+v", status)
						}
					} else {
						var status protocol.Status
						if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
							t.Fatal(err)
						}
						if status.SetupRequired != (version == "") {
							t.Fatalf("setupRequired=%v for version %q", status.SetupRequired, version)
						}
					}
				}
			}
			if checks.Load() != 0 || installer.probes.Load() != 0 {
				t.Fatalf("control polling performed disk checks=%d version probes=%d", checks.Load(), installer.probes.Load())
			}
		})
	}
}

type observationInstaller struct{ probes atomic.Int64 }

func (*observationInstaller) Prepare(context.Context, core.InstallRequest) (core.PreparedCore, error) {
	return nil, errors.New("unexpected install during a read-only query")
}

func (i *observationInstaller) DetectVersion(context.Context, string) (string, error) {
	i.probes.Add(1)
	return "v9.99.0", nil
}
