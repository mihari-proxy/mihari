package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

func TestManagerSettings_NewClonesCallerOwnedMutableValues(t *testing.T) {
	settings := config.Defaults()
	settings.Logging = &config.LoggingSettings{Level: "info"}
	settings.Tun = map[string]any{"nested": map[string]any{"enabled": true}}

	manager := newTestManager(Options{Settings: settings})
	settings.Logging.Level = "debug"
	settings.Tun["nested"].(map[string]any)["enabled"] = false

	snapshot := manager.settingsSnapshot()
	if snapshot.Logging.Level != "info" {
		t.Fatalf("logging level=%q want info", snapshot.Logging.Level)
	}
	if got := snapshot.Tun["nested"].(map[string]any)["enabled"]; got != true {
		t.Fatalf("nested tun value=%v want true", got)
	}
}

func TestManagerSettings_PrepareDoesNotPublishAndRejectsInvalidCandidate(t *testing.T) {
	manager := newTestManager(Options{Settings: config.Defaults()})
	before := manager.settingsSnapshot()

	candidate, err := manager.prepareSettings(func(settings *config.Settings) error {
		settings.WebAddr = "127.0.0.1:9190"
		return nil
	})
	if err == nil {
		t.Fatal("expected validation failure")
	}
	if candidate.changed {
		t.Fatalf("invalid candidate=%#v", candidate)
	}
	if got := manager.settingsSnapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("prepare published settings: got=%#v want=%#v", got, before)
	}
}

func TestManagerSettings_SaveCandidateOutcomes(t *testing.T) {
	t.Run("pre-commit failure keeps memory", func(t *testing.T) {
		settings := config.Defaults()
		settings.Tun = map[string]any{"nested": map[string]any{"enabled": true}}
		manager := newTestManager(Options{
			Settings: settings, SettingsPath: "settings.yaml",
			SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
				return config.CommitResult{}, errors.New("replace failed")
			},
		})
		before := manager.settingsSnapshot()
		candidate, err := manager.updateSettings(func(next *config.Settings) error {
			next.Tun["nested"].(map[string]any)["enabled"] = false
			return nil
		})
		if err == nil {
			t.Fatal("expected persist failure")
		}
		if candidate.changed {
			t.Fatalf("candidate=%#v", candidate)
		}
		if got := manager.settingsSnapshot(); !reflect.DeepEqual(got, before) {
			t.Fatalf("failed save changed memory: got=%#v want=%#v", got, before)
		}
	})

}

func TestManagerSettings_PostCommitWarning(t *testing.T) {
	var reports atomic.Int64
	var component, message string
	manager := newTestManager(Options{
		Settings: config.Defaults(), SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			return config.CommitResult{Committed: true, Warning: errors.New("C:\\sensitive\\path")}, nil
		},
		OnBackgroundError: func(gotComponent string, err error) {
			reports.Add(1)
			component, message = gotComponent, err.Error()
		},
	})
	candidate, err := manager.updateSettings(func(next *config.Settings) error {
		next.WebAddr = "127.0.0.1:9999"
		return nil
	})
	if err != nil || !candidate.changed {
		t.Fatalf("candidate=%#v err=%v", candidate, err)
	}
	if got := manager.settingsSnapshot().WebAddr; got != "127.0.0.1:9999" {
		t.Fatalf("published web address=%q", got)
	}
	if reports.Load() != 1 || component != "settings" || message != "parent directory sync failed after commit" {
		t.Fatalf("warning report count=%d component=%q message=%q", reports.Load(), component, message)
	}
}

func TestManagerSettings_SaveDoesNotHoldSettingsMutex(t *testing.T) {
	saveStarted := make(chan struct{})
	releaseSave := make(chan struct{})
	manager := newTestManager(Options{
		Settings: config.Defaults(), SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			close(saveStarted)
			<-releaseSave
			return config.CommitResult{Committed: true}, nil
		},
	})
	candidate, err := manager.prepareSettings(func(next *config.Settings) error {
		next.WebAddr = "127.0.0.1:9999"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	saved := make(chan error, 1)
	go func() {
		_, saveErr := manager.saveSettingsCandidate(candidate)
		saved <- saveErr
	}()
	<-saveStarted
	read := make(chan config.Settings, 1)
	go func() { read <- manager.settingsSnapshot() }()
	select {
	case snapshot := <-read:
		if snapshot.WebAddr == "127.0.0.1:9999" {
			t.Fatalf("save published candidate before caller chose to publish")
		}
	case <-time.After(time.Second):
		t.Fatal("settings snapshot blocked on saver")
	}
	close(releaseSave)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
}

func TestManagerSettings_NoOpDoesNotSaveOrPublish(t *testing.T) {
	var saves atomic.Int64
	manager := newTestManager(Options{
		Settings: config.Defaults(), SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	candidate, err := manager.updateSettings(func(*config.Settings) error { return nil })
	if err != nil || candidate.changed || saves.Load() != 0 {
		t.Fatalf("candidate=%#v err=%v saves=%d", candidate, err, saves.Load())
	}
}

func TestManagerSettings_RestorePublishesOnlyAfterCommittedRollback(t *testing.T) {
	settings := config.Defaults()
	manager := newTestManager(Options{
		Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, saved config.Settings) (config.CommitResult, error) {
			if saved.WebAddr == settings.WebAddr {
				return config.CommitResult{}, errors.New("rollback did not commit")
			}
			return config.CommitResult{Committed: true}, nil
		},
	})
	if _, err := manager.updateSettings(func(next *config.Settings) error {
		next.WebAddr = "127.0.0.1:9999"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.restoreSettings(settings); err == nil {
		t.Fatal("expected rollback failure")
	}
	if got := manager.settingsSnapshot().WebAddr; got != "127.0.0.1:9999" {
		t.Fatalf("uncommitted rollback published %q", got)
	}
}

func TestManagerSettings_MapsInvalidSaverOutcome(t *testing.T) {
	manager := newTestManager(Options{
		Settings: config.Defaults(), SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			return config.CommitResult{Committed: true}, errors.New("ambiguous")
		},
	})
	_, err := manager.updateSettings(func(next *config.Settings) error {
		next.WebAddr = "127.0.0.1:9999"
		return nil
	})
	var apiError protocol.APIError
	if err == nil || !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || !strings.Contains(err.Error(), "persist settings") {
		t.Fatalf("err=%v", err)
	}
}

func TestManagerSettings_DegradedMutationRejectsQueuedOperationButAllowsObservation(t *testing.T) {
	controller := &fakeController{}
	manager := newTestManager(Options{Controller: controller, Settings: config.Defaults()})
	if err := manager.lockMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}

	queued := make(chan error, 1)
	go func() {
		queued <- manager.SelectProxy(context.Background(), Operation{ID: "queued", Source: "test"}, "group", "proxy")
	}()

	degradeResult, degradeErr := manager.doOperation(context.Background(), "degrade", func() (any, error) {
		if err := manager.enterMutationDegraded(&state.Snapshot{}); err == nil {
			return nil, errors.New("expected committed error")
		} else {
			return nil, err
		}
	})
	if degradeResult != nil || degradeErr == nil {
		t.Fatalf("degrade result=%v err=%v", degradeResult, degradeErr)
	}
	manager.unlock()

	select {
	case err := <-queued:
		var apiError protocol.APIError
		if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidState {
			t.Fatalf("queued err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued mutation did not finish")
	}
	if calls := controller.callsFor("select"); len(calls) != 0 {
		t.Fatalf("degraded mutation reached controller: %#v", calls)
	}

	_, retryErr := manager.doOperation(context.Background(), "degrade", func() (any, error) {
		return nil, errors.New("must not execute retry")
	})
	var retryAPIError protocol.APIError
	if !errors.As(retryErr, &retryAPIError) || retryAPIError.Code != protocol.CodeDataFailure {
		t.Fatalf("retry err=%v", retryErr)
	}

	before := manager.Snapshot().Revision
	manager.setCoreState(state.CoreState{Status: "running"})
	if got := manager.Snapshot(); got.Revision != before+1 || got.Core.Status != "running" {
		t.Fatalf("background observation=%#v before=%d", got, before)
	}
	if _, err := manager.Proxies(context.Background()); err != nil {
		t.Fatalf("read-only operation blocked while degraded: %v", err)
	}
}

func TestManagerStatusMethodsWaitForMaintenance(t *testing.T) {
	sysProxyEntered := make(chan struct{})
	sysProxyManager := newTestManager(Options{
		SysProxy: &maintenanceProbeSysProxy{entered: sysProxyEntered},
	})
	tunEntered := make(chan struct{})
	tunManager := newTestManager(Options{
		Controller: &maintenanceProbeController{fakeController: &fakeController{}, entered: tunEntered},
	})
	webGUIEntered := make(chan struct{})
	webGUIManager := newTestManager(Options{
		Panels:     &fakePanels{},
		WebGateway: &maintenanceProbeGateway{entered: webGUIEntered},
	})
	geoIPEntered := make(chan struct{})
	var geoIPOnce sync.Once
	geoIPManager := newTestManager(Options{
		GeoIP: &fakeGeoIPService{statusFunc: func() geoip.Status {
			geoIPOnce.Do(func() { close(geoIPEntered) })
			return geoip.Status{}
		}},
	})

	tests := []struct {
		name    string
		manager *Manager
		entered <-chan struct{}
		status  func(context.Context) error
	}{
		{
			name:    "system proxy",
			manager: sysProxyManager,
			entered: sysProxyEntered,
			status: func(ctx context.Context) error {
				_, err := sysProxyManager.SystemProxyStatus(ctx)
				return err
			},
		},
		{
			name:    "tun",
			manager: tunManager,
			entered: tunEntered,
			status: func(ctx context.Context) error {
				_, err := tunManager.TunStatus(ctx)
				return err
			},
		},
		{
			name:    "web gui",
			manager: webGUIManager,
			entered: webGUIEntered,
			status: func(ctx context.Context) error {
				_, err := webGUIManager.WebGUIStatus(ctx)
				return err
			},
		},
		{
			name:    "geoip",
			manager: geoIPManager,
			entered: geoIPEntered,
			status: func(ctx context.Context) error {
				_, err := geoIPManager.GeoIPStatus(ctx)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.manager.lockMaintenance(context.Background()); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() { done <- tt.status(context.Background()) }()

			select {
			case <-tt.entered:
				tt.manager.unlock()
				t.Fatalf("status reached its dependency while maintenance was held")
			case err := <-done:
				tt.manager.unlock()
				t.Fatalf("status returned while maintenance was held: %v", err)
			case <-time.After(100 * time.Millisecond):
			}

			tt.manager.unlock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("status did not resume after maintenance was released")
			}
		})
	}
}

type maintenanceProbeSysProxy struct {
	entered chan struct{}
	once    sync.Once
}

func (p *maintenanceProbeSysProxy) Get() (sysproxy.State, error) {
	p.once.Do(func() { close(p.entered) })
	return sysproxy.State{}, nil
}

func (*maintenanceProbeSysProxy) Enable(string, int) error { return nil }

func (*maintenanceProbeSysProxy) Disable() error { return nil }

type maintenanceProbeController struct {
	*fakeController
	entered chan struct{}
	once    sync.Once
}

func (p *maintenanceProbeController) Configs(context.Context) (map[string]any, error) {
	p.once.Do(func() { close(p.entered) })
	return map[string]any{}, nil
}

type maintenanceProbeGateway struct {
	entered chan struct{}
	once    sync.Once
}

func (*maintenanceProbeGateway) Serve(context.Context) error { return nil }

func (*maintenanceProbeGateway) SessionCount() int { return 0 }

func (p *maintenanceProbeGateway) ListenAddr() string {
	p.once.Do(func() { close(p.entered) })
	return "127.0.0.1:9191"
}
