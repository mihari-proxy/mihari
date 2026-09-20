package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

func TestStartupNetwork_SystemProxyApplyingDuringOSWrite(t *testing.T) {
	for _, desired := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(stringBool(desired)+"/failure="+stringBool(fail), func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				ctx, cancel := context.WithCancel(context.Background())
				backend := &hookSysProxyBackend{state: sysproxy.State{Enabled: true, Server: "127.0.0.1:7890"}}
				var once sync.Once
				block := func() { once.Do(func() { close(entered); <-release }) }
				if desired {
					backend.onEnable = block
				} else {
					backend.onDisable = block
				}
				if fail {
					backend.enableErr, backend.disableErr = errors.New("OS write failed"), errors.New("OS write failed")
				}
				service, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json")})
				if err != nil {
					t.Fatal(err)
				}
				supervised := make(chan struct{})
				m := newTestManager(Options{SysProxy: backend, Onboarding: service, Settings: config.Settings{Schema: "mihari.settings/v1", MixedAddr: "127.0.0.1:7890", SystemProxyDesired: desired}, Supervisor: &fakeSupervisor{run: func(ctx context.Context) error {
					close(supervised)
					<-ctx.Done()
					return nil
				}}})
				done := make(chan error, 1)
				go func() { done <- m.Run(ctx) }()
				t.Cleanup(func() {
					cancel()
					select {
					case <-release:
					default:
						close(release)
					}
					<-done
				})
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("startup never reached OS write")
				}
				status := m.StartupNetworkStatus()
				if status == nil || !status.SystemProxyApplying || status.TunApplying {
					t.Errorf("during OS write: %+v", status)
				}
				probeCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
				defer stop()
				if required, err := m.SetupRequired(probeCtx); err != nil || required {
					t.Errorf("readiness blocked or changed during apply: required=%v err=%v", required, err)
				}
				// The one-shot hook lets shutdown cleanup proceed.
				close(release)
				select {
				case <-supervised:
				case <-time.After(3 * time.Second):
					t.Fatal("supervisor was not reached")
				}
				if got := m.StartupNetworkStatus(); got != nil {
					t.Errorf("completed startup still applying: %+v", got)
				}
			})
		}
	}
}

func stringBool(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func TestStartupNetwork_TunInitialAttemptOnly(t *testing.T) {
	for _, terminal := range []supervisor.Status{supervisor.StatusRunning, supervisor.StatusBackoff, supervisor.StatusDegraded, supervisor.StatusStopped} {
		t.Run(string(terminal), func(t *testing.T) {
			for _, desired := range []bool{false, true} {
				var m *Manager
				m = newTestManager(Options{SysProxy: &sysproxy.FakeBackend{}, Settings: config.Settings{Schema: "mihari.settings/v1", MixedAddr: "127.0.0.1:7890", Tun: map[string]any{"enable": desired}}, Supervisor: &fakeSupervisor{run: func(context.Context) error {
					if got := m.StartupNetworkStatus(); got == nil || !got.TunApplying || got.SystemProxyApplying {
						t.Errorf("initial attempt: %+v", got)
					}
					m.Observe(supervisor.Observation{Status: supervisor.StatusStarting})
					if got := m.StartupNetworkStatus(); got == nil || !got.TunApplying {
						t.Errorf("lost applying while starting: %+v", got)
					}
					m.Observe(supervisor.Observation{Status: terminal})
					if got := m.StartupNetworkStatus(); got != nil {
						t.Errorf("terminal attempt still applying: %+v", got)
					}
					m.Observe(supervisor.Observation{Status: supervisor.StatusStarting, Restarts: 1})
					if got := m.StartupNetworkStatus(); got != nil {
						t.Errorf("later restart triggered startup badge: %+v", got)
					}
					return nil
				}}})
				if err := m.Run(context.Background()); err != nil {
					t.Fatal(err)
				}
				if got := m.StartupNetworkStatus(); got != nil {
					t.Errorf("Run returned while applying: %+v", got)
				}
			}
		})
	}
}

func TestStartupNetwork_UnmanagedAndStandaloneRestoreStayIdle(t *testing.T) {
	var m *Manager
	m = newTestManager(Options{SysProxy: &sysproxy.FakeBackend{}, Settings: config.Settings{Schema: "mihari.settings/v1", MixedAddr: "127.0.0.1:7890"}, Supervisor: &fakeSupervisor{run: func(context.Context) error {
		if got := m.StartupNetworkStatus(); got != nil {
			t.Errorf("unmanaged TUN applying: %+v", got)
		}
		return nil
	}}})
	if err := m.ApplyDesiredSystemProxy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.StartupNetworkStatus(); got != nil {
		t.Errorf("standalone restore marked startup: %+v", got)
	}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStartupNetwork_RunExitClearsUnfinishedTun(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(stringBool(canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			settings := config.Defaults()
			settings.Tun = map[string]any{"enable": true}
			var m *Manager
			m = newTestManager(Options{Settings: settings, SysProxy: &sysproxy.FakeBackend{}, Supervisor: &fakeSupervisor{run: func(context.Context) error {
				if got := m.StartupNetworkStatus(); got == nil || !got.TunApplying {
					t.Fatal("initial TUN not applying")
				}
				if canceled {
					cancel()
					return ctx.Err()
				}
				return errors.New("supervisor failed before observation")
			}}})
			err := m.Run(ctx)
			if (err == nil) != canceled {
				t.Fatalf("canceled=%v err=%v", canceled, err)
			}
			if got := m.StartupNetworkStatus(); got != nil {
				t.Fatalf("finished Run still applying: %+v", got)
			}
		})
	}
}

type startupWaitingSupervisor struct{ fakeSupervisor }

func (*startupWaitingSupervisor) WaitForCore() {}

func TestStartupNetwork_MissingOrRepairRequiredCoreStaysIdle(t *testing.T) {
	for _, repair := range []bool{false, true} {
		settings := config.Defaults()
		settings.Tun = map[string]any{"enable": true}
		var m *Manager
		s := &startupWaitingSupervisor{fakeSupervisor{run: func(context.Context) error {
			if got := m.StartupNetworkStatus(); got != nil {
				t.Errorf("blocked core applying: %+v", got)
			}
			return nil
		}}}
		m = newTestManager(Options{Settings: settings, SysProxy: &sysproxy.FakeBackend{}, Supervisor: s, CoreRepairRequired: repair, BinaryExists: func() bool { return repair }})
		if err := m.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSetupReadiness_RestartRequiredRemainsVisibleWhileGateHeld(t *testing.T) {
	service, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json")})
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(Options{Onboarding: service, SysProxy: &sysproxy.FakeBackend{}})
	m.onboardingRestartRequired.Store(true)
	if err := m.lockMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if required, err := m.SetupRequired(ctx); err != nil || !required {
		t.Fatalf("required=%v err=%v", required, err)
	}
}
