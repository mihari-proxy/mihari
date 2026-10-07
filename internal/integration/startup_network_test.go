package integration

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"github.com/mihari-proxy/mihari/internal/tundetect"
)

type startupProxyBackend struct {
	sysproxy.FakeBackend
	once    sync.Once
	entered chan struct{}
	release chan struct{}
	failure error
}

func (b *startupProxyBackend) apply() error {
	b.once.Do(func() { close(b.entered); <-b.release })
	return b.failure
}
func (b *startupProxyBackend) Enable(host string, port int) error {
	if err := b.apply(); err != nil {
		return err
	}
	return b.FakeBackend.Enable(host, port)
}
func (b *startupProxyBackend) Disable() error {
	if err := b.apply(); err != nil {
		return err
	}
	return b.FakeBackend.Disable()
}

type startupCoreSupervisor struct {
	manager                   *runtimeapi.Manager
	entered, release, settled chan struct{}
}

func (s *startupCoreSupervisor) Run(ctx context.Context) error {
	s.manager.Observe(supervisor.Observation{Status: supervisor.StatusStarting})
	close(s.entered)
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.manager.Observe(supervisor.Observation{Status: supervisor.StatusRunning})
	close(s.settled)
	<-ctx.Done()
	return nil
}
func (*startupCoreSupervisor) Restart(context.Context) error { return nil }

func TestStartupNetwork_StatusOverIPCDuringApplication(t *testing.T) {
	for _, desired := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(map[bool]string{false: "off", true: "on"}[desired]+"/"+map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
				settings := config.Defaults()
				settings.SystemProxyDesired = desired
				settings.Tun = map[string]any{"enable": desired}
				backend := &startupProxyBackend{entered: make(chan struct{}), release: make(chan struct{})}
				backend.State = sysproxy.State{Enabled: true, Server: settings.MixedAddr}
				if fail {
					backend.failure = errors.New("simulated OS write failure")
				}
				core := &startupCoreSupervisor{entered: make(chan struct{}), release: make(chan struct{}), settled: make(chan struct{})}
				setup, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json")})
				if err != nil {
					t.Fatal(err)
				}
				store := state.NewStore(state.Snapshot{Revision: 7, Health: "ok"})
				manager := runtimeapi.New(runtimeapi.Options{Store: store, Coordinator: state.NewCoordinator(store), SysProxy: backend, TunDetect: &tundetect.FakeBackend{}, Supervisor: core, Onboarding: setup, Settings: settings, BinaryExists: func() bool { return true }})
				core.manager = manager
				endpoint := transporttest.Endpoint(t)
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() {
					done <- daemon.Run(ctx, daemon.Options{Endpoint: endpoint, Token: "startup-test", Store: store, Runtime: manager})
				}()
				t.Cleanup(func() {
					cancel()
					select {
					case <-backend.release:
					default:
						close(backend.release)
					}
					select {
					case err := <-done:
						if err != nil {
							t.Errorf("daemon exit: %v", err)
						}
					case <-time.After(5 * time.Second):
						t.Error("daemon did not stop")
					}
				})
				select {
				case <-backend.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("startup did not reach OS write")
				}
				client := controlclient.New(endpoint, "startup-test")
				probe, stop := context.WithTimeout(context.Background(), time.Second)
				status, err := client.Status(probe)
				stop()
				if err != nil {
					t.Fatalf("status waited for the OS write: %v", err)
				}
				if status.SetupRequired || status.Revision != 7 || status.StartupNetwork == nil || !status.StartupNetwork.SystemProxyApplying || status.StartupNetwork.TunApplying {
					t.Fatalf("OS application status: %+v", status)
				}
				close(backend.release)
				select {
				case <-core.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("core startup not reached")
				}
				probe, stop = context.WithTimeout(context.Background(), time.Second)
				status, err = client.Status(probe)
				stop()
				if err != nil || status.StartupNetwork == nil || status.StartupNetwork.SystemProxyApplying || !status.StartupNetwork.TunApplying {
					t.Fatalf("core application status: %+v err=%v", status, err)
				}
				close(core.release)
				select {
				case <-core.settled:
				case <-time.After(3 * time.Second):
					t.Fatal("core startup not settled")
				}
				probe, stop = context.WithTimeout(context.Background(), time.Second)
				status, err = client.Status(probe)
				stop()
				if err != nil || status.StartupNetwork != nil {
					t.Fatalf("settled status: %+v err=%v", status, err)
				}
			})
		}
	}
}
