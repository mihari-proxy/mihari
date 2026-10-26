package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

type endpointIPCService struct {
	restart func() error
	calls   int
}

func (s *endpointIPCService) ManagedDaemonPID(context.Context) (int, error) { return os.Getpid(), nil }
func (s *endpointIPCService) Restart() error                                { s.calls++; return s.restart() }

func TestEndpointRestartIPC_AppliesSavedSettingsAfterOwnerReplacement(t *testing.T) {
	root := t.TempDir()
	settingsPath := filepath.Join(root, "settings.yaml")
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := config.SaveWithCommit(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	endpoint := transporttest.Endpoint(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	start := func(settings config.Settings, startedAt time.Time) (func() error, error) {
		ob, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(root, "onboarding.json")})
		if err != nil {
			return nil, err
		}
		store := state.NewStore(state.Snapshot{Health: "ok", StartedAt: startedAt, Core: state.CoreState{Status: "missing"}})
		manager := runtimeapi.New(runtimeapi.Options{Store: store, Settings: settings, SettingsPath: settingsPath, Onboarding: ob})
		listener, err := transport.Listen(endpoint)
		if err != nil {
			return nil, err
		}
		serverCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		control := server.New(server.Options{Token: "fixture-token", Store: store, Runtime: manager})
		go func() { done <- control.Serve(serverCtx, listener) }()
		var once sync.Once
		var stopErr error
		return func() error {
			once.Do(func() {
				stop()
				select {
				case stopErr = <-done:
				case <-time.After(5 * time.Second):
					stopErr = errors.New("control server shutdown timed out")
				}
			})
			return stopErr
		}, nil
	}
	stop, err := start(settings, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Error(err)
		}
	})
	client := controlclient.New(endpoint, "fixture-token")
	mixed := "127.0.0.1:19190"
	saved, err := client.UpdateOnboarding(ctx, protocol.OnboardingUpdateRequest{OperationID: "change-mixed-port", MixedAddr: &mixed})
	if err != nil || !saved.RestartRequired {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	service := &endpointIPCService{restart: func() error {
		if err := stop(); err != nil {
			return err
		}
		current, err := config.Load(settingsPath)
		if err != nil {
			return err
		}
		nextStop, err := start(current, time.Unix(101, 0))
		if err != nil {
			return err
		}
		stop = nextStop
		return nil
	}}
	result, err := app.RestartSavedEndpoints(ctx, saved, app.EndpointRestartOptions{Client: client, Service: service, Elevated: func() bool { return true }})
	if err != nil || service.calls != 1 || result.Ports.RestartRequired || result.Ports.MixedAddr != mixed || !result.Status.StartedAt.Equal(time.Unix(101, 0)) {
		t.Fatalf("result=%+v restarts=%d err=%v", result, service.calls, err)
	}
	final, err := config.Load(settingsPath)
	if err != nil || final.MixedAddr != mixed || final.ControllerAddr != settings.ControllerAddr || final.WebAddr != settings.WebAddr {
		t.Fatalf("unexpected persisted endpoints: mixed=%s controller=%s web=%s err=%v", final.MixedAddr, final.ControllerAddr, final.WebAddr, err)
	}
}
