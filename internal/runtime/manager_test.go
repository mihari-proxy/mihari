package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/preferences"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"github.com/mihari-proxy/mihari/internal/tundetect"
)

func TestUpdateOnboardingRejectsStaleRevisionBeforePersistingEndpoints(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	var stateSaves, settingsSaves atomic.Int64
	service, err := onboarding.Open(onboarding.Options{
		StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: true,
		SaveState: func(string, onboarding.State) (config.CommitResult, error) {
			stateSaves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stateSaves.Store(0)
	manager := newTestManager(Options{
		Onboarding: service, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			settingsSaves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	manager.store.Store(state.Snapshot{Revision: 3})
	webAddr := "127.0.0.1:9292"
	stale := uint64(2)
	_, err = manager.UpdateOnboarding(context.Background(), Operation{ID: "setup-stale", Source: "test", IfRevision: &stale}, onboarding.Update{WebAddr: &webAddr})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	if settingsSaves.Load() != 0 || stateSaves.Load() != 0 {
		t.Fatalf("stale update writes: settings=%d state=%d", settingsSaves.Load(), stateSaves.Load())
	}
	if got := manager.settingsSnapshot().WebAddr; got != settings.WebAddr {
		t.Fatalf("stale update published web address=%q", got)
	}

	current := uint64(3)
	status, err := manager.UpdateOnboarding(context.Background(), Operation{ID: "setup-current", Source: "test", IfRevision: &current}, onboarding.Update{WebAddr: &webAddr})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status.WebAddr != webAddr || !status.Status.RestartRequired || status.Revision != 4 || manager.Snapshot().Revision != 4 {
		t.Fatalf("status=%#v revision=%d", status, manager.Snapshot().Revision)
	}
	if settingsSaves.Load() != 1 || stateSaves.Load() != 0 {
		t.Fatalf("endpoint-only writes: settings=%d state=%d", settingsSaves.Load(), stateSaves.Load())
	}
}

func TestOnboarding_InvalidEndpointRejectsBeforePersistence(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	var stateSaves, settingsSaves atomic.Int64
	service, err := onboarding.Open(onboarding.Options{
		StatePath: filepath.Join(t.TempDir(), "onboarding.json"),
		SaveState: func(string, onboarding.State) (config.CommitResult, error) {
			stateSaves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stateSaves.Store(0)
	manager := newTestManager(Options{
		Onboarding: service, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			settingsSaves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	invalid := "0.0.0.0:9090"
	if _, err := manager.UpdateOnboarding(context.Background(), Operation{ID: "setup-invalid-endpoint", Source: "test"}, onboarding.Update{ControllerAddr: &invalid}); err == nil {
		t.Fatal("non-loopback controller was accepted")
	}
	if settingsSaves.Load() != 0 || stateSaves.Load() != 0 || manager.settingsSnapshot().ControllerAddr != settings.ControllerAddr {
		t.Fatalf("writes: settings=%d state=%d current=%q", settingsSaves.Load(), stateSaves.Load(), manager.settingsSnapshot().ControllerAddr)
	}
}

func TestOnboarding_SettingsAndStateCommitBeforePublish(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	settings.SetLogging(config.LoggingSettings{Level: "debug", MaxSizeMB: 20, MaxFiles: 5})
	beforeWeb := settings.WebAddr
	webAddr, complete := "127.0.0.1:9292", true
	var manager *Manager
	var order []string
	var stateCalls int
	var warnings []string
	service, err := onboarding.Open(onboarding.Options{
		StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: true,
		SaveState: func(_ string, saved onboarding.State) (config.CommitResult, error) {
			stateCalls++
			if stateCalls == 1 {
				return config.CommitResult{Committed: true}, nil
			}
			order = append(order, "state")
			if got := manager.settingsSnapshot().WebAddr; got != beforeWeb {
				t.Fatalf("settings published before state commit: %q", got)
			}
			if !saved.Complete {
				t.Fatalf("saved state=%#v", saved)
			}
			return config.CommitResult{Committed: true, Warning: errors.New("C:\\sensitive\\onboarding.json")}, nil
		},
		OnPersistenceWarning: func(err error) { warnings = append(warnings, err.Error()) },
	})
	if err != nil {
		t.Fatal(err)
	}
	var savedSettings config.Settings
	manager = newTestManager(Options{
		Onboarding: service, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, saved config.Settings) (config.CommitResult, error) {
			order = append(order, "settings")
			if got := manager.settingsSnapshot().WebAddr; got != beforeWeb {
				t.Fatalf("settings published during candidate save: %q", got)
			}
			savedSettings = saved.Clone()
			return config.CommitResult{Committed: true}, nil
		},
	})

	got, err := manager.UpdateOnboarding(context.Background(), Operation{ID: "setup-ordered", Source: "test"}, onboarding.Update{
		Complete: &complete, WebAddr: &webAddr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"settings", "state"}) {
		t.Fatalf("commit order=%v", order)
	}
	if savedSettings.WebAddr != webAddr || savedSettings.EffectiveLogging() != settings.EffectiveLogging() {
		t.Fatalf("saved settings=%#v", savedSettings)
	}
	if got.Status.WebAddr != webAddr || !got.Status.Complete || !got.Status.RestartRequired || got.Revision != 1 {
		t.Fatalf("status=%#v", got)
	}
	if current := manager.settingsSnapshot(); current.WebAddr != webAddr || current.EffectiveLogging() != settings.EffectiveLogging() {
		t.Fatalf("published settings=%#v", current)
	}
	if len(warnings) != 1 || warnings[0] != "onboarding parent directory sync failed after commit" {
		t.Fatalf("warnings=%v", warnings)
	}
}

func TestOnboarding_StatePreCommitFailureRollsBackSettingsBeforePublish(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	disk := settings.Clone()
	webAddr, complete := "127.0.0.1:9292", true
	var stateCalls int
	service, err := onboarding.Open(onboarding.Options{
		StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: true,
		SaveState: func(string, onboarding.State) (config.CommitResult, error) {
			stateCalls++
			if stateCalls == 1 {
				return config.CommitResult{Committed: true}, nil
			}
			return config.CommitResult{}, errors.New("replace C:\\sensitive\\onboarding.json")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var saved []string
	var warnings []string
	manager := newTestManager(Options{
		Onboarding: service, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			disk = candidate.Clone()
			saved = append(saved, candidate.WebAddr)
			if len(saved) == 2 {
				return config.CommitResult{Committed: true, Warning: errors.New("C:\\sensitive\\settings.yaml")}, nil
			}
			return config.CommitResult{Committed: true}, nil
		},
		OnBackgroundError: func(component string, err error) {
			warnings = append(warnings, component+":"+err.Error())
		},
	})

	_, err = manager.UpdateOnboarding(context.Background(), Operation{ID: "setup-state-fail", Source: "test"}, onboarding.Update{
		Complete: &complete, WebAddr: &webAddr,
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "persist settings" || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("err=%v api=%#v", err, apiError)
	}
	if !reflect.DeepEqual(saved, []string{webAddr, settings.WebAddr}) {
		t.Fatalf("settings saves=%v", saved)
	}
	if disk.WebAddr != settings.WebAddr || manager.settingsSnapshot().WebAddr != settings.WebAddr {
		t.Fatalf("disk=%q memory=%q want before=%q", disk.WebAddr, manager.settingsSnapshot().WebAddr, settings.WebAddr)
	}
	if !reflect.DeepEqual(warnings, []string{"settings:parent directory sync failed after commit"}) {
		t.Fatalf("warnings=%v", warnings)
	}
	status, statusErr := manager.OnboardingStatus(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Status.Complete || status.Status.RestartRequired || status.Revision != 0 || manager.Snapshot().Health != "ok" {
		t.Fatalf("status=%#v snapshot=%#v", status, manager.Snapshot())
	}
}

func TestOnboarding_CompensationFailureCommitsDegraded(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	disk := settings.Clone()
	webAddr, complete := "127.0.0.1:9292", true
	var stateCalls int
	service, err := onboarding.Open(onboarding.Options{
		StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: true,
		SaveState: func(string, onboarding.State) (config.CommitResult, error) {
			stateCalls++
			if stateCalls == 1 {
				return config.CommitResult{Committed: true}, nil
			}
			return config.CommitResult{}, errors.New("state replace failed")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var settingsSaves int
	manager := newTestManager(Options{
		Onboarding: service, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			settingsSaves++
			if settingsSaves == 2 {
				return config.CommitResult{}, errors.New("settings rollback failed")
			}
			disk = candidate.Clone()
			return config.CommitResult{Committed: true}, nil
		},
	})

	_, err = manager.UpdateOnboarding(context.Background(), Operation{ID: "setup-compensation-fail", Source: "test"}, onboarding.Update{
		Complete: &complete, WebAddr: &webAddr,
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "mutation compensation failed" {
		t.Fatalf("err=%v api=%#v", err, apiError)
	}
	if disk.WebAddr != webAddr || manager.settingsSnapshot().WebAddr != webAddr {
		t.Fatalf("disk=%q memory=%q want after=%q", disk.WebAddr, manager.settingsSnapshot().WebAddr, webAddr)
	}
	status, statusErr := manager.OnboardingStatus(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Status.Complete || !status.Status.RestartRequired || status.Status.WebAddr != webAddr {
		t.Fatalf("status=%#v", status)
	}
	snapshot := manager.Snapshot()
	if snapshot.Revision != 1 || snapshot.Health != "degraded" || snapshot.LastError != "mutation compensation failed; restart required" {
		t.Fatalf("snapshot=%#v", snapshot)
	}

	nextWeb := "127.0.0.1:9393"
	_, err = manager.UpdateOnboarding(context.Background(), Operation{ID: "setup-after-degraded", Source: "test"}, onboarding.Update{WebAddr: &nextWeb})
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidState || settingsSaves != 2 {
		t.Fatalf("later err=%v api=%#v saves=%d", err, apiError, settingsSaves)
	}
}

func TestOnboarding_CommittedSettingsIgnoresLateCancellation(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	webAddr, complete := "127.0.0.1:9292", true
	ctx, cancel := context.WithCancel(context.Background())
	var settingsSaves, stateCalls int
	service, err := onboarding.Open(onboarding.Options{
		StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: true,
		SaveState: func(string, onboarding.State) (config.CommitResult, error) {
			stateCalls++
			return config.CommitResult{Committed: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stateCalls = 0
	manager := newTestManager(Options{
		Onboarding: service, Settings: settings, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			settingsSaves++
			cancel()
			return config.CommitResult{Committed: true}, nil
		},
	})
	operation := Operation{ID: "setup-cancelled-after-commit", Source: "test"}
	wantUpdate := onboarding.Update{Complete: &complete, WebAddr: &webAddr}
	got, err := manager.UpdateOnboarding(ctx, operation, wantUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1 || !got.Status.Complete || got.Status.WebAddr != webAddr || settingsSaves != 1 || stateCalls != 1 {
		t.Fatalf("status=%#v settings saves=%d state saves=%d", got, settingsSaves, stateCalls)
	}
	retry, err := manager.UpdateOnboarding(context.Background(), operation, wantUpdate)
	if err != nil || !reflect.DeepEqual(retry, got) || settingsSaves != 1 || stateCalls != 1 {
		t.Fatalf("retry=%#v err=%v settings saves=%d state saves=%d", retry, err, settingsSaves, stateCalls)
	}
}

func TestOnboardingStatus_ComposesEndpointsFromManagerSettings(t *testing.T) {
	service, err := onboarding.Open(onboarding.Options{
		StatePath: filepath.Join(t.TempDir(), "onboarding.json"),
		SaveState: func(string, onboarding.State) (config.CommitResult, error) {
			return config.CommitResult{Committed: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.MixedAddr = "127.0.0.1:19190"
	settings.ControllerAddr = "127.0.0.1:19090"
	settings.WebAddr = "127.0.0.1:19191"
	manager := newTestManager(Options{Onboarding: service, Settings: settings})

	got, err := manager.OnboardingStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Status.Complete || got.Status.MixedAddr != settings.MixedAddr || got.Status.ControllerAddr != settings.ControllerAddr || got.Status.WebAddr != settings.WebAddr {
		t.Fatalf("status=%#v", got)
	}
}

func TestUpdateTUIPreferencesCommitsThroughCoordinator(t *testing.T) {
	service, err := preferences.Open(filepath.Join(t.TempDir(), "tui.json"))
	if err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(Options{Preferences: service})
	want := []string{"host", "chain", "traffic"}
	got, err := manager.UpdateTUIPreferences(context.Background(), Operation{
		ID: "columns-1", Source: "test",
	}, preferences.Update{ConnectionsColumns: want})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ConnectionsColumns, want) || manager.Snapshot().Revision != 1 {
		t.Fatalf("preferences=%#v revision=%d", got, manager.Snapshot().Revision)
	}

	stale := uint64(0)
	_, err = manager.UpdateTUIPreferences(context.Background(), Operation{
		ID: "columns-stale", Source: "test", IfRevision: &stale,
	}, preferences.Update{ConnectionsColumns: []string{"source"}})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	if got := service.Snapshot().ConnectionsColumns; !reflect.DeepEqual(got, want) {
		t.Fatalf("stale update persisted columns=%v", got)
	}
}

func TestGeoIPUpdatePreparesOutsideCoordinatorAndRejectsStaleRevision(t *testing.T) {
	prepared := &fakeGeoIPCandidate{valid: true, identity: "pair-1"}
	service := &fakeGeoIPService{}
	manager := newTestManager(Options{
		GeoIP:        service,
		PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) { return prepared, nil },
	})
	manager.store.Store(state.Snapshot{Revision: 3})
	stale := uint64(2)
	_, err := manager.UpdateGeoIP(context.Background(), Operation{ID: "geoip-stale", Source: "test", IfRevision: &stale})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	if prepared.commits != 0 || prepared.cleanups != 1 {
		t.Fatalf("commits=%d cleanups=%d", prepared.commits, prepared.cleanups)
	}
	if service.recordedError {
		t.Fatal("revision conflict degraded geoip health")
	}

	current := uint64(3)
	prepared = &fakeGeoIPCandidate{valid: true, identity: "pair-2"}
	status, err := manager.UpdateGeoIP(context.Background(), Operation{ID: "geoip-current", Source: "test", IfRevision: &current})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.commits != 1 || status.Country.Available != true || manager.Snapshot().Revision != 4 {
		t.Fatalf("candidate=%#v status=%#v revision=%d", prepared, status, manager.Snapshot().Revision)
	}
}

func TestGeoIPUpdateFailureIsRecordedWithoutReplacingCurrentDatabases(t *testing.T) {
	service := &fakeGeoIPService{}
	manager := newTestManager(Options{
		GeoIP:        service,
		PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) { return nil, errors.New("download failed with secret") },
	})
	_, err := manager.UpdateGeoIP(context.Background(), Operation{ID: "geoip-failed", Source: "test"})
	if err == nil || !service.recordedError {
		t.Fatalf("err=%v recorded=%v", err, service.recordedError)
	}
}

func TestGeoIPStaleCandidateDoesNotDegradeNewerDatabaseHealth(t *testing.T) {
	service := &fakeGeoIPService{}
	candidate := &fakeGeoIPCandidate{valid: true, identity: "stale", commitErr: geoip.ErrStaleCandidate}
	manager := newTestManager(Options{
		GeoIP:        service,
		PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) { return candidate, nil },
	})
	_, err := manager.UpdateGeoIP(context.Background(), Operation{ID: "geoip-stale-candidate", Source: "test"})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict || service.recordedError {
		t.Fatalf("err=%v recorded=%v", err, service.recordedError)
	}
}

func TestManagerCancelsOwnedSchedulerWhenSupervisorStops(t *testing.T) {
	schedulerStopped := make(chan struct{})
	manager := newTestManager(Options{
		Supervisor: &fakeSupervisor{run: func(context.Context) error { return errors.New("stopped") }},
		RunScheduler: func(ctx context.Context) error {
			<-ctx.Done()
			close(schedulerStopped)
			return nil
		},
	})
	done := make(chan error, 1)
	go func() { done <- manager.Run(context.Background()) }()
	select {
	case <-schedulerStopped:
	case <-time.After(3 * time.Second):
		t.Fatal("owned scheduler was not canceled")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("supervisor error was lost")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("manager did not return after scheduler stopped")
	}
}

type errorGateway struct {
	err error
}

func (g errorGateway) Serve(context.Context) error { return g.err }
func (errorGateway) SessionCount() int             { return 0 }
func (errorGateway) ListenAddr() string            { return "127.0.0.1:0" }

func TestManagerReportsWebGatewayError(t *testing.T) {
	var gotComponent string
	var gotErr error
	manager := newTestManager(Options{
		Supervisor: &fakeSupervisor{run: func(context.Context) error { return errors.New("stopped") }},
		WebGateway: errorGateway{err: errors.New("listen failed")},
		OnBackgroundError: func(component string, err error) {
			gotComponent, gotErr = component, err
		},
	})
	_ = manager.Run(context.Background())
	if gotComponent != "web-gateway" || gotErr == nil || gotErr.Error() != "listen failed" {
		t.Fatalf("component=%q err=%v", gotComponent, gotErr)
	}
}

func TestManagerIgnoresWebGatewayCancellation(t *testing.T) {
	called := false
	manager := newTestManager(Options{
		Supervisor:        &fakeSupervisor{run: func(context.Context) error { return errors.New("stopped") }},
		WebGateway:        errorGateway{err: context.Canceled},
		OnBackgroundError: func(string, error) { called = true },
	})
	_ = manager.Run(context.Background())
	if called {
		t.Fatal("cancellation reported as background error")
	}
}

func TestManagerReportsSchedulerError(t *testing.T) {
	var gotComponent string
	var gotErr error
	manager := newTestManager(Options{
		Supervisor: &fakeSupervisor{run: func(context.Context) error { return errors.New("stopped") }},
		RunScheduler: func(context.Context) error {
			return errors.New("refresh failed")
		},
		OnBackgroundError: func(component string, err error) {
			gotComponent, gotErr = component, err
		},
	})
	_ = manager.Run(context.Background())
	if gotComponent != "scheduler" || gotErr == nil || gotErr.Error() != "refresh failed" {
		t.Fatalf("component=%q err=%v", gotComponent, gotErr)
	}
}

func TestManagerIgnoresSchedulerCancellation(t *testing.T) {
	called := false
	manager := newTestManager(Options{
		Supervisor: &fakeSupervisor{run: func(context.Context) error { return errors.New("stopped") }},
		RunScheduler: func(context.Context) error {
			return context.Canceled
		},
		OnBackgroundError: func(string, error) { called = true },
	})
	_ = manager.Run(context.Background())
	if called {
		t.Fatal("scheduler cancellation reported as background error")
	}
}

type fakeGeoIPCandidate struct {
	valid     bool
	identity  string
	commits   int
	cleanups  int
	commitErr error
}

func (c *fakeGeoIPCandidate) Identity() string { return c.identity }
func (c *fakeGeoIPCandidate) Valid() bool      { return c.valid }
func (c *fakeGeoIPCandidate) Commit() error    { c.commits++; return c.commitErr }
func (c *fakeGeoIPCandidate) Cleanup()         { c.cleanups++ }

type fakeGeoIPService struct {
	statusFunc    func() geoip.Status
	recordedError bool
}

func (s *fakeGeoIPService) Status() geoip.Status {
	if s.statusFunc != nil {
		return s.statusFunc()
	}
	return geoip.Status{Country: geoip.DatabaseStatus{Available: true}, ASN: geoip.DatabaseStatus{Available: true}}
}
func (*fakeGeoIPService) Lookup(netip.Addr) (geoip.Record, error) { return geoip.Record{}, nil }
func (s *fakeGeoIPService) RecordUpdateError(err error)           { s.recordedError = err != nil }

func TestInstallCommitAndRestartCannotOverlap(t *testing.T) {
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	restartCalled := make(chan struct{})
	manager := newTestManager(Options{
		Installer: &fakeInstaller{candidate: &fakeCandidate{
			version: "v1.19.0",
			commit: func() (core.InstallResult, error) {
				close(commitEntered)
				<-releaseCommit
				return core.InstallResult{Version: "v1.19.0", Updated: true}, nil
			},
		}},
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			close(restartCalled)
			return nil
		}},
	})
	installDone := make(chan error, 1)
	go func() {
		_, err := manager.Install(context.Background(), Operation{ID: "install-1", Source: "test"})
		installDone <- err
	}()
	select {
	case <-commitEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("install did not enter commit")
	}
	restartDone := make(chan error, 1)
	go func() {
		restartDone <- manager.Restart(context.Background(), Operation{ID: "restart-1", Source: "test"})
	}()
	select {
	case <-restartCalled:
		t.Fatal("restart overlapped install commit")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseCommit)
	if err := <-installDone; err != nil {
		t.Fatal(err)
	}
	if err := <-restartDone; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeMutationDoesNotWaitForSupervisorRestart(t *testing.T) {
	restartEntered := make(chan struct{})
	releaseRestart := make(chan struct{})
	selectEntered := make(chan struct{}, 1)
	manager := newTestManager(Options{
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			close(restartEntered)
			<-releaseRestart
			return nil
		}},
		Controller: &fakeController{entered: selectEntered},
	})
	restartDone := make(chan error, 1)
	go func() { restartDone <- manager.Restart(context.Background(), Operation{ID: "restart", Source: "test"}) }()
	select {
	case <-restartEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not begin")
	}
	mutationCtx := &doneObservedContext{Context: context.Background(), observed: make(chan struct{})}
	selectDone := make(chan error, 1)
	go func() {
		selectDone <- manager.SelectProxy(mutationCtx, Operation{ID: "select", Source: "test"}, "GLOBAL", "DIRECT")
	}()
	select {
	case <-selectEntered:
	case <-time.After(3 * time.Second):
		close(releaseRestart)
		<-restartDone
		t.Fatal("proxy selection remained blocked while supervisor restart was in progress")
	}
	close(releaseRestart)
	if err := <-restartDone; err != nil {
		t.Fatal(err)
	}
	if err := <-selectDone; err != nil {
		t.Fatal(err)
	}
}

type doneObservedContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (c *doneObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

type controllerMutationContextKey struct{}

func TestControllerMutationsCommitRevisionAndPropagateErrors(t *testing.T) {
	controllerErr := errors.New("controller mutation failed")
	tests := []struct {
		name     string
		kind     string
		wantArgs []string
		setError func(*fakeController, error)
		invoke   func(context.Context, *Manager, Operation) error
	}{
		{
			name:     "select proxy",
			kind:     "select",
			wantArgs: []string{"GLOBAL", "DIRECT"},
			setError: func(controller *fakeController, err error) { controller.selectProxyErr = err },
			invoke: func(ctx context.Context, manager *Manager, operation Operation) error {
				return manager.SelectProxy(ctx, operation, "GLOBAL", "DIRECT")
			},
		},
		{
			name:     "close connection",
			kind:     "close",
			wantArgs: []string{"connection-1"},
			setError: func(controller *fakeController, err error) { controller.closeConnectionErr = err },
			invoke: func(ctx context.Context, manager *Manager, operation Operation) error {
				return manager.CloseConnection(ctx, operation, "connection-1")
			},
		},
		{
			name:     "close all connections",
			kind:     "close-all",
			wantArgs: nil,
			setError: func(controller *fakeController, err error) { controller.closeAllConnectionsErr = err },
			invoke: func(ctx context.Context, manager *Manager, operation Operation) error {
				return manager.CloseAllConnections(ctx, operation)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name+" success", func(t *testing.T) {
			controller := &fakeController{}
			manager := newTestManager(Options{Controller: controller})
			ctx := context.WithValue(context.Background(), controllerMutationContextKey{}, test.name)

			if err := test.invoke(ctx, manager, Operation{ID: "success", Source: "test"}); err != nil {
				t.Fatal(err)
			}
			if revision := manager.Snapshot().Revision; revision != 1 {
				t.Fatalf("revision=%d want=1", revision)
			}
			requireControllerCall(t, controller, test.kind, ctx, test.wantArgs)
		})

		t.Run(test.name+" error", func(t *testing.T) {
			controller := &fakeController{}
			test.setError(controller, controllerErr)
			manager := newTestManager(Options{Controller: controller})
			ctx := context.WithValue(context.Background(), controllerMutationContextKey{}, test.name)

			err := test.invoke(ctx, manager, Operation{ID: "error", Source: "test"})
			if !errors.Is(err, controllerErr) {
				t.Fatalf("err=%v want controller error", err)
			}
			if revision := manager.Snapshot().Revision; revision != 0 {
				t.Fatalf("revision=%d want=0", revision)
			}
			requireControllerCall(t, controller, test.kind, ctx, test.wantArgs)
		})
	}
}

func TestControllerMutationsSettleAfterSuccessfulCallCancelsRequest(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		invoke func(context.Context, *Manager, Operation) error
	}{
		{
			name: "select proxy",
			kind: "select",
			invoke: func(ctx context.Context, manager *Manager, operation Operation) error {
				return manager.SelectProxy(ctx, operation, "GLOBAL", "DIRECT")
			},
		},
		{
			name: "close connection",
			kind: "close",
			invoke: func(ctx context.Context, manager *Manager, operation Operation) error {
				return manager.CloseConnection(ctx, operation, "connection-1")
			},
		},
		{
			name: "close all connections",
			kind: "close-all",
			invoke: func(ctx context.Context, manager *Manager, operation Operation) error {
				return manager.CloseAllConnections(ctx, operation)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			controller := &fakeController{afterCall: cancel}
			manager := newTestManager(Options{Controller: controller})
			operation := Operation{ID: "cancel-after-success", Source: "test"}

			if err := test.invoke(ctx, manager, operation); err != nil {
				t.Fatalf("first call: %v", err)
			}
			if revision := manager.Snapshot().Revision; revision != 1 {
				t.Fatalf("revision after first call=%d want=1", revision)
			}
			if err := test.invoke(context.Background(), manager, operation); err != nil {
				t.Fatalf("cached retry: %v", err)
			}
			if calls := controller.callsFor(test.kind); len(calls) != 1 {
				t.Fatalf("%s calls=%d want=1", test.kind, len(calls))
			}
			if controller.callsFor(test.kind)[0].ctx != ctx {
				t.Fatal("controller call did not receive the original request context")
			}
		})
	}
}

func TestControllerMutationsSettleAfterConcurrentCoordinatorRevision(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	controller := &fakeController{entered: entered, release: release}
	manager := newTestManager(Options{Controller: controller})
	current := uint64(0)
	mutationDone := make(chan error, 1)
	go func() {
		mutationDone <- manager.SelectProxy(context.Background(), Operation{
			ID: "select-with-concurrent-state", Source: "test", IfRevision: &current,
		}, "GLOBAL", "DIRECT")
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("controller mutation did not begin")
	}

	coordinatorStarted := make(chan struct{})
	coordinatorDone := make(chan error, 1)
	go func() {
		close(coordinatorStarted)
		_, err := manager.coordinator.Do(context.Background(), state.CommandMeta{Source: "concurrent-test"}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			return snapshot, nil
		})
		coordinatorDone <- err
	}()
	<-coordinatorStarted
	select {
	case err := <-coordinatorDone:
		if err != nil {
			close(release)
			<-mutationDone
			t.Fatalf("concurrent coordinator mutation: %v", err)
		}
	case <-time.After(3 * time.Second):
		close(release)
		<-mutationDone
		<-coordinatorDone
		t.Fatal("coordinator mutation remained blocked while controller I/O was in progress")
	}

	close(release)
	err := <-mutationDone
	if err != nil {
		t.Fatalf("controller mutation: %v", err)
	}
	if revision := manager.Snapshot().Revision; revision != 2 {
		t.Fatalf("revision=%d want=2", revision)
	}
}

func TestControllerMutationsRejectStaleRevisionBeforeControllerIO(t *testing.T) {
	controller := &fakeController{}
	manager := newTestManager(Options{Controller: controller})
	manager.store.Store(state.Snapshot{Revision: 2})
	stale := uint64(1)

	err := manager.CloseAllConnections(context.Background(), Operation{
		ID: "stale-close-all", Source: "test", IfRevision: &stale,
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v want revision conflict", err)
	}
	if calls := controller.callsFor("close-all"); len(calls) != 0 {
		t.Fatalf("close-all calls=%d want=0", len(calls))
	}
	if revision := manager.Snapshot().Revision; revision != 2 {
		t.Fatalf("revision=%d want=2", revision)
	}
}

func TestControllerMutationsDeduplicateConcurrentOperationID(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	controller := &fakeController{entered: entered, release: release}
	manager := newTestManager(Options{Controller: controller})
	operation := Operation{ID: "same-select", Source: "test"}
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- manager.SelectProxy(context.Background(), operation, "GLOBAL", "DIRECT")
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("controller mutation did not begin")
	}

	secondCtx := &doneObservedContext{Context: context.Background(), observed: make(chan struct{})}
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- manager.SelectProxy(secondCtx, operation, "GLOBAL", "DIRECT")
	}()
	select {
	case <-secondCtx.observed:
	case <-time.After(3 * time.Second):
		t.Fatal("duplicate mutation did not enter existing-operation wait")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if calls := controller.callsFor("select"); len(calls) != 1 {
		t.Fatalf("select calls=%d want=1", len(calls))
	}
	if revision := manager.Snapshot().Revision; revision != 1 {
		t.Fatalf("revision=%d want=1", revision)
	}
}

func requireControllerCall(t *testing.T, controller *fakeController, kind string, wantContext context.Context, wantArgs []string) {
	t.Helper()
	calls := controller.callsFor(kind)
	if len(calls) != 1 {
		t.Fatalf("%s calls=%d want=1", kind, len(calls))
	}
	if calls[0].ctx != wantContext {
		t.Fatalf("%s context was not propagated", kind)
	}
	if !reflect.DeepEqual(calls[0].args, wantArgs) {
		t.Fatalf("%s args=%v want=%v", kind, calls[0].args, wantArgs)
	}
}

func TestUpdateRuleProviderUsesCoordinatorAndRevision(t *testing.T) {
	store := state.NewStore(state.Snapshot{Revision: 7})
	var updated []string
	manager := New(Options{
		Store:        store,
		Coordinator:  state.NewCoordinator(store),
		BinaryExists: func() bool { return true },
		Controller: &fakeController{updateRuleProvider: func(_ context.Context, name string) error {
			updated = append(updated, name)
			return nil
		}},
	})
	stale := uint64(6)
	err := manager.UpdateRuleProvider(context.Background(), Operation{ID: "stale", Source: "test", IfRevision: &stale}, "OpenAI")
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict || len(updated) != 0 {
		t.Fatalf("err=%v updated=%v", err, updated)
	}
	current := uint64(7)
	operation := Operation{ID: "provider-1", Source: "test", IfRevision: &current}
	if err := manager.UpdateRuleProvider(context.Background(), operation, "OpenAI"); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateRuleProvider(context.Background(), operation, "OpenAI"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated, []string{"OpenAI"}) || manager.Snapshot().Revision != 8 {
		t.Fatalf("updated=%v revision=%d", updated, manager.Snapshot().Revision)
	}
}

func TestDuplicateOperationIDExecutesOnce(t *testing.T) {
	prepareEntered := make(chan struct{})
	releasePrepare := make(chan struct{})
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0"}}
	installer.prepare = func(context.Context, core.InstallRequest) (PreparedCore, error) {
		close(prepareEntered)
		<-releasePrepare
		return installer.candidate, nil
	}
	manager := newTestManager(Options{Installer: installer, Supervisor: &fakeSupervisor{}})
	results := make(chan core.InstallResult, 2)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			result, err := manager.Install(context.Background(), Operation{ID: "same-operation", Source: "test"})
			results <- result
			errors <- err
		}()
	}
	select {
	case <-prepareEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("prepare did not begin")
	}
	close(releasePrepare)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if result := <-results; result.Version != "v1.19.0" {
			t.Fatalf("result=%#v", result)
		}
	}
	if got := installer.calls.Load(); got != 1 {
		t.Fatalf("prepare calls=%d", got)
	}
}

func TestInstallRevisionConflictDoesNotCommitCandidate(t *testing.T) {
	var commits atomic.Int64
	manager := newTestManager(Options{
		Installer: &fakeInstaller{candidate: &fakeCandidate{
			version: "v1.19.0",
			commit: func() (core.InstallResult, error) {
				commits.Add(1)
				return core.InstallResult{Version: "v1.19.0", Updated: true}, nil
			},
		}},
		Supervisor: &fakeSupervisor{},
	})
	stale := uint64(99)
	_, err := manager.Install(context.Background(), Operation{ID: "stale-install", Source: "test", IfRevision: &stale})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	if commits.Load() != 0 {
		t.Fatal("stale install committed its candidate")
	}
}

func TestInstallUsesCurrentVersionAndSkipsRestartWhenUnchanged(t *testing.T) {
	requestSeen := make(chan core.InstallRequest, 1)
	var restarts atomic.Int64
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0", notUpdated: true}}
	installer.prepare = func(_ context.Context, request core.InstallRequest) (PreparedCore, error) {
		requestSeen <- request
		return installer.candidate, nil
	}
	manager := newTestManager(Options{
		Installer: installer,
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			restarts.Add(1)
			return nil
		}},
	})
	snapshot := manager.store.Load()
	snapshot.Core.Version = "v1.19.0"
	manager.store.Store(snapshot)
	manager.running.Store(true)
	result, err := manager.Install(context.Background(), Operation{ID: "up-to-date", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if request := <-requestSeen; request.CurrentVersion != "v1.19.0" {
		t.Fatalf("current version=%q", request.CurrentVersion)
	}
	if result.Updated || restarts.Load() != 0 {
		t.Fatalf("result=%#v restarts=%d", result, restarts.Load())
	}
}

func TestInstallCoalescesSettingsChannelWhenOperationChannelNil(t *testing.T) {
	requestSeen := make(chan core.InstallRequest, 1)
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0"}}
	installer.prepare = func(_ context.Context, request core.InstallRequest) (PreparedCore, error) {
		requestSeen <- request
		return installer.candidate, nil
	}
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	settings.CoreChannel = "alpha"
	manager := newTestManager(Options{Installer: installer, Settings: settings, Supervisor: &fakeSupervisor{}})
	snapshot := manager.store.Load()
	snapshot.Core.AlphaSHA = "e183c58"
	manager.store.Store(snapshot)

	if _, err := manager.Install(context.Background(), Operation{ID: "coalesce-settings", Source: "test"}); err != nil {
		t.Fatal(err)
	}
	request := <-requestSeen
	if request.Channel != "alpha" {
		t.Fatalf("request.Channel=%q want alpha", request.Channel)
	}
	if request.AlphaSHA != "e183c58" {
		t.Fatalf("request.AlphaSHA=%q want e183c58", request.AlphaSHA)
	}
}

func TestInstallDoesNotPersistChannelWhenPrepareFails(t *testing.T) {
	requestSeen := make(chan core.InstallRequest, 1)
	installer := &fakeInstaller{}
	installer.prepare = func(_ context.Context, request core.InstallRequest) (PreparedCore, error) {
		requestSeen <- request
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "prepare failed"}
	}
	settings, settingsPath := persistedChannelSettings(t, "stable")
	manager := newTestManager(Options{Installer: installer, Settings: settings, SettingsPath: settingsPath})
	channel := "alpha"
	_, err := manager.Install(context.Background(), Operation{ID: "switch-fail", Source: "test", Channel: &channel})
	if err == nil {
		t.Fatal("expected prepare error")
	}
	request := <-requestSeen
	if request.Channel != "alpha" {
		t.Fatalf("request.Channel=%q want alpha", request.Channel)
	}
	loaded, err := config.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CoreChannel != "stable" {
		t.Fatalf("settings.CoreChannel=%q want stable", loaded.CoreChannel)
	}
	if got := manager.store.Load().Core.Channel; got != "" {
		t.Fatalf("store.Core.Channel=%q want empty", got)
	}
}

func TestInstall_CoreCommitThenSettingsSaveFailureRecordsIdentityAndDegrades(t *testing.T) {
	settings, settingsPath := persistedChannelSettings(t, "stable")
	var commits atomic.Int64
	var saves atomic.Int64
	var restarts atomic.Int64
	manager := newTestManager(Options{
		Installer: &fakeInstaller{candidate: &fakeCandidate{
			version:  "v1.20.0",
			alphaSHA: "alpha-new",
			commit: func() (core.InstallResult, error) {
				commits.Add(1)
				return core.InstallResult{Version: "v1.20.0", Updated: true, AlphaSHA: "alpha-new"}, nil
			},
		}},
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			restarts.Add(1)
			return nil
		}},
		Settings: settings, SettingsPath: settingsPath,
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves.Add(1)
			return config.CommitResult{}, errors.New(`replace failed at C:\secret\mihari.yaml`)
		},
	})
	before := state.Snapshot{Revision: 9, Health: "ok"}
	before.Core.Version = "v1.19.0"
	before.Core.Channel = "stable"
	manager.store.Store(before)
	alpha := "alpha"
	op := Operation{ID: "commit-then-settings-fail", Source: "test", Channel: &alpha}

	_, err := manager.Install(context.Background(), op)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "mutation compensation failed" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err=%v want sanitized committed data failure", err)
	}
	if commits.Load() != 1 || saves.Load() != 1 {
		t.Fatalf("commits=%d saves=%d want 1/1", commits.Load(), saves.Load())
	}
	if got := manager.settingsSnapshot(); !reflect.DeepEqual(got, settings) {
		t.Fatalf("memory settings changed: core channel=%q", got.CoreChannel)
	}
	loaded, loadErr := config.Load(settingsPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(loaded, settings) {
		t.Fatalf("disk settings changed: core channel=%q", loaded.CoreChannel)
	}
	snapshot := manager.Snapshot()
	if snapshot.Revision != 10 || snapshot.Health != "degraded" || snapshot.LastError != "mutation compensation failed; restart required" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Core.Version != "v1.20.0" || snapshot.Core.Channel != "alpha" || snapshot.Core.AlphaSHA != "alpha-new" {
		t.Fatalf("committed core identity=%#v", snapshot.Core)
	}
	if nextErr := manager.Restart(context.Background(), Operation{ID: "after-core-degraded", Source: "test"}); !errors.As(nextErr, &apiError) || apiError.Code != protocol.CodeInvalidState || restarts.Load() != 0 {
		t.Fatalf("next mutation err=%v restarts=%d", nextErr, restarts.Load())
	}
	if _, retryErr := manager.Install(context.Background(), op); retryErr == nil || retryErr.Error() != err.Error() {
		t.Fatalf("retry err=%v want cached %v", retryErr, err)
	}
	if commits.Load() != 1 || saves.Load() != 1 || manager.Snapshot().Revision != 10 {
		t.Fatalf("retry repeated settlement: commits=%d saves=%d revision=%d", commits.Load(), saves.Load(), manager.Snapshot().Revision)
	}
}

func TestInstallCommitPersistsChannelAndClearsAlphaSHAOnStable(t *testing.T) {
	installer := &fakeInstaller{candidate: &fakeCandidate{
		version:  "v1.19.0",
		alphaSHA: "e183c58",
	}}
	settings, settingsPath := persistedChannelSettings(t, "stable")
	manager := newTestManager(Options{
		Installer: installer, Settings: settings, SettingsPath: settingsPath, Supervisor: &fakeSupervisor{},
	})
	alpha := "alpha"
	if _, err := manager.Install(context.Background(), Operation{ID: "alpha-install", Source: "test", Channel: &alpha}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CoreChannel != "alpha" {
		t.Fatalf("settings.CoreChannel=%q want alpha", loaded.CoreChannel)
	}
	core := manager.store.Load().Core
	if core.Version != "v1.19.0" || core.Channel != "alpha" || core.AlphaSHA != "e183c58" {
		t.Fatalf("after alpha install core=%#v", core)
	}

	installer.candidate = &fakeCandidate{version: "v1.19.1"}
	stable := "stable"
	if _, err := manager.Install(context.Background(), Operation{ID: "stable-install", Source: "test", Channel: &stable}); err != nil {
		t.Fatal(err)
	}
	loaded, err = config.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CoreChannel != "stable" {
		t.Fatalf("settings.CoreChannel=%q want stable", loaded.CoreChannel)
	}
	core = manager.store.Load().Core
	if core.Channel != "stable" || core.AlphaSHA != "" {
		t.Fatalf("after stable install core=%#v", core)
	}
}

func TestInstallCoreChannelPreservesIndependentSettings(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	settings.Tun = map[string]any{
		"enable": false,
		"stack":  "system",
		"dns":    map[string]any{"nameserver": []any{"1.1.1.1"}},
	}
	settings.SetLogging(config.LoggingSettings{Level: "debug", MaxSizeMB: 20, MaxFiles: 5})

	var saved []config.Settings
	manager := newTestManager(Options{
		Installer:    &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0", alphaSHA: "e183c58"}},
		Supervisor:   &fakeSupervisor{},
		Settings:     settings,
		SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, candidate config.Settings) (config.CommitResult, error) {
			saved = append(saved, candidate.Clone())
			return config.CommitResult{Committed: true}, nil
		},
	})
	alpha := "alpha"
	if _, err := manager.Install(context.Background(), Operation{ID: "channel-settings", Source: "test", Channel: &alpha}); err != nil {
		t.Fatal(err)
	}

	if len(saved) != 1 {
		t.Fatalf("settings saves=%d want=1", len(saved))
	}
	for label, got := range map[string]config.Settings{
		"saved":     saved[0],
		"published": manager.settingsSnapshot(),
	} {
		if got.CoreChannel != "alpha" {
			t.Fatalf("%s core channel=%q want alpha", label, got.CoreChannel)
		}
		if !reflect.DeepEqual(got.Tun, settings.Tun) {
			t.Fatalf("%s tun=%#v want %#v", label, got.Tun, settings.Tun)
		}
		if effective := got.EffectiveLogging(); effective != settings.EffectiveLogging() {
			t.Fatalf("%s logging=%#v want %#v", label, effective, settings.EffectiveLogging())
		}
	}
}

func TestInstallSetupSidecarRejectsStaleRevisionBeforeSettings(t *testing.T) {
	binDir := t.TempDir()
	binaryPath := filepath.Join(binDir, "mihomo")
	if err := os.WriteFile(binaryPath, []byte("placeholder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "core-channel"), []byte("alpha\nalpha-e183c58\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	settings.Tun = map[string]any{"enable": false, "stack": "system"}
	settings.SetLogging(config.LoggingSettings{Level: "debug", MaxSizeMB: 20, MaxFiles: 5})
	var saves atomic.Int64
	installer := &fakeInstaller{detectVersion: func(context.Context, string) (string, error) {
		return "v1.18.0", nil
	}}
	manager := newTestManager(Options{
		Installer:    installer,
		Settings:     settings,
		SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	manager.installRequest.BinaryPath = binaryPath
	manager.store.Store(state.Snapshot{Revision: 3, Health: "ok"})
	stale := uint64(2)

	_, err := manager.Install(context.Background(), Operation{
		ID: "setup-sidecar-stale", Source: "setup", IfRevision: &stale,
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v want revision conflict", err)
	}
	if saves.Load() != 0 {
		t.Fatalf("settings saves=%d want=0 before stale revision is rejected", saves.Load())
	}
	got := manager.settingsSnapshot()
	if got.CoreChannel != "stable" || got.CoreChannelBundle != "" {
		t.Fatalf("stale sidecar published channel=%q bundle=%q", got.CoreChannel, got.CoreChannelBundle)
	}
	if !reflect.DeepEqual(got.Tun, settings.Tun) || got.EffectiveLogging() != settings.EffectiveLogging() {
		t.Fatalf("stale sidecar changed independent settings: %#v", got)
	}
	if revision := manager.Snapshot().Revision; revision != 3 {
		t.Fatalf("revision=%d want=3", revision)
	}
}

func TestInstallReleasesGateBeforeSupervisorRestartSettings(t *testing.T) {
	restartEntered := make(chan struct{})
	releaseRestart := make(chan struct{})
	selectEntered := make(chan struct{}, 1)
	manager := newTestManager(Options{
		Installer: &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0"}},
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			close(restartEntered)
			<-releaseRestart
			return nil
		}},
		Controller: &fakeController{entered: selectEntered},
	})
	manager.running.Store(true)
	installDone := make(chan error, 1)
	go func() {
		_, err := manager.Install(context.Background(), Operation{ID: "install-restart", Source: "test"})
		installDone <- err
	}()
	select {
	case <-restartEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("install did not enter supervisor restart")
	}

	selectDone := make(chan error, 1)
	go func() {
		selectDone <- manager.SelectProxy(context.Background(), Operation{ID: "during-install-restart", Source: "test"}, "GLOBAL", "DIRECT")
	}()
	select {
	case <-selectEntered:
	case <-time.After(3 * time.Second):
		close(releaseRestart)
		<-installDone
		t.Fatal("runtime mutation remained blocked while install waited for supervisor restart")
	}
	close(releaseRestart)
	if err := <-installDone; err != nil {
		t.Fatal(err)
	}
	if err := <-selectDone; err != nil {
		t.Fatal(err)
	}
}

func TestObservePreservesCoreChannelAndAlphaSHA(t *testing.T) {
	installer := &fakeInstaller{candidate: &fakeCandidate{
		version:  "v1.19.0",
		alphaSHA: "e183c58",
	}}
	settings, settingsPath := persistedChannelSettings(t, "stable")
	manager := newTestManager(Options{
		Installer: installer, Settings: settings, SettingsPath: settingsPath, Supervisor: &fakeSupervisor{},
	})
	alpha := "alpha"
	if _, err := manager.Install(context.Background(), Operation{ID: "alpha-install", Source: "test", Channel: &alpha}); err != nil {
		t.Fatal(err)
	}

	manager.Observe(supervisor.Observation{Status: supervisor.StatusRunning, PID: 4242})

	core := manager.store.Load().Core
	if core.Status != string(supervisor.StatusRunning) || core.PID != 4242 {
		t.Fatalf("observe did not apply runtime fields: %#v", core)
	}
	if core.Version != "v1.19.0" || core.Channel != "alpha" || core.AlphaSHA != "e183c58" {
		t.Fatalf("observe wiped identity: %#v", core)
	}
}

func TestObserveWaitingForInstallPreservesCommittedCoreIdentity(t *testing.T) {
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	installer := &fakeInstaller{candidate: &fakeCandidate{
		version:  "v1.20.0",
		alphaSHA: "alpha-new",
		commit: func() (core.InstallResult, error) {
			close(commitEntered)
			<-releaseCommit
			return core.InstallResult{Version: "v1.20.0", Updated: true, AlphaSHA: "alpha-new"}, nil
		},
	}}
	settings, settingsPath := persistedChannelSettings(t, "stable")
	manager := newTestManager(Options{
		Installer: installer, Settings: settings, SettingsPath: settingsPath, Supervisor: &fakeSupervisor{},
	})
	before := manager.store.Load()
	before.Core.Version = "v1.19.0"
	before.Core.Channel = "stable"
	manager.store.Store(before)

	alpha := "alpha"
	installDone := make(chan error, 1)
	go func() {
		_, err := manager.Install(context.Background(), Operation{ID: "identity-install", Source: "test", Channel: &alpha})
		installDone <- err
	}()
	select {
	case <-commitEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("install did not enter candidate commit")
	}

	observeDone := make(chan struct{})
	go func() {
		manager.Observe(supervisor.Observation{Status: supervisor.StatusRunning, PID: 4242, Restarts: 2})
		close(observeDone)
	}()
	waitForRuntimeStack(t, "(*Manager).Observe", "(*Manager).lockMaintenance")
	close(releaseCommit)
	if err := <-installDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-observeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("observation did not finish after install")
	}

	got := manager.store.Load().Core
	if got.Status != string(supervisor.StatusRunning) || got.PID != 4242 || got.Restarts != 2 {
		t.Fatalf("observation dynamic fields=%#v", got)
	}
	if got.Version != "v1.20.0" || got.Channel != "alpha" || got.AlphaSHA != "alpha-new" {
		t.Fatalf("delayed observation overwrote committed identity: %#v", got)
	}
}

func waitForRuntimeStack(t *testing.T, frames ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var stack string
		for size := 1 << 16; ; size *= 2 {
			buffer := make([]byte, size)
			n := goruntime.Stack(buffer, true)
			if n < len(buffer) {
				stack = string(buffer[:n])
				break
			}
		}
		matched := true
		for _, frame := range frames {
			if !strings.Contains(stack, frame) {
				matched = false
				break
			}
		}
		if matched {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("runtime stack did not contain frames %q", frames)
}

func TestInstallRejectsNightlyChannelBeforePrepare(t *testing.T) {
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0"}}
	settings, settingsPath := persistedChannelSettings(t, "stable")
	manager := newTestManager(Options{Installer: installer, Settings: settings, SettingsPath: settingsPath})
	nightly := "nightly"
	_, err := manager.Install(context.Background(), Operation{ID: "nightly", Source: "test", Channel: &nightly})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("err=%v", err)
	}
	if got := installer.calls.Load(); got != 0 {
		t.Fatalf("Prepare called %d times", got)
	}
	loaded, err := config.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CoreChannel != "stable" {
		t.Fatalf("settings.CoreChannel=%q want stable", loaded.CoreChannel)
	}
}

func persistedChannelSettings(t *testing.T, channel string) (config.Settings, string) {
	t.Helper()
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	settings.CoreChannel = channel
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := config.Save(path, settings); err != nil {
		t.Fatal(err)
	}
	return settings, path
}

func TestMissingCoreStaysControllableAndStartsAfterInstall(t *testing.T) {
	var exists atomic.Bool
	supervisorStarted := make(chan struct{})
	manager := newTestManager(Options{
		BinaryExists: func() bool { return exists.Load() },
		Installer: &fakeInstaller{candidate: &fakeCandidate{
			version: "v1.19.0",
			commit: func() (core.InstallResult, error) {
				exists.Store(true)
				return core.InstallResult{Version: "v1.19.0", Updated: true}, nil
			},
		}},
		Supervisor: &fakeSupervisor{run: func(ctx context.Context) error {
			close(supervisorStarted)
			<-ctx.Done()
			return nil
		}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	requireCoreStatus(t, manager.store, string(supervisor.Status("missing")))
	if _, err := manager.Install(context.Background(), Operation{ID: "install", Source: "test"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-supervisorStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not start after install")
	}
	cancel()
	if err := waitManager(t, done); err != nil {
		t.Fatal(err)
	}
	err := manager.SelectProxy(context.Background(), Operation{ID: "after-stop", Source: "test"}, "GLOBAL", "DIRECT")
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidState {
		t.Fatalf("post-shutdown err=%v", err)
	}
}

func TestManagerLocalCoreReflectsDetectVersion(t *testing.T) {
	installer := &fakeInstaller{detectVersion: func(context.Context, string) (string, error) {
		return "v1.18.5", nil
	}}
	manager := newTestManager(Options{Installer: installer})
	info, err := manager.LocalCore(context.Background())
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !info.Ready || info.Version != "v1.18.5" {
		t.Fatalf("info=%#v", info)
	}
	if got := installer.detectCalls.Load(); got != 1 {
		t.Fatalf("detect calls=%d", got)
	}

	missing := &fakeInstaller{detectVersion: func(context.Context, string) (string, error) {
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "missing"}
	}}
	manager = newTestManager(Options{Installer: missing})
	info, err = manager.LocalCore(context.Background())
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if info.Ready || info.Version != "" {
		t.Fatalf("missing core should not be ready: %#v", info)
	}

	// No installer wired (e.g. minimal runtime) must report not-ready, never error.
	none := newTestManager(Options{})
	if info, err := none.LocalCore(context.Background()); err != nil || info.Ready {
		t.Fatalf("nil installer err=%v info=%#v", err, info)
	}
}

func newTestManager(options Options) *Manager {
	store := state.NewStore(state.Snapshot{Health: "ok"})
	options.Store = store
	options.Coordinator = state.NewCoordinator(store)
	if options.BinaryExists == nil {
		options.BinaryExists = func() bool { return true }
	}
	// 默认注入空 FakeBackend：避免开发机上 Platform() 枚举到真实 TUN 网卡而误伤现有测试。
	if options.TunDetect == nil {
		options.TunDetect = &tundetect.FakeBackend{}
	}
	if options.LookupTCPOccupant == nil {
		options.LookupTCPOccupant = func(string) (int, bool) { return 0, false }
	}
	manager := New(options)
	manager.store = store
	return manager
}

func TestManagerServiceStatusDelegatesToInjectedFunc(t *testing.T) {
	running := newTestManager(Options{ServiceStatus: func() (string, error) {
		return "running", nil
	}})
	status, err := running.ServiceStatus(context.Background())
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if status.Schema != "mihari/v1" || status.Status != "running" {
		t.Fatalf("status=%#v", status)
	}

	failing := newTestManager(Options{ServiceStatus: func() (string, error) {
		return "", protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: "scm offline"}
	}})
	if status, err := failing.ServiceStatus(context.Background()); err != nil || status.Status != "unknown" {
		t.Fatalf("error should resolve to unknown: status=%#v err=%v", status, err)
	}

	// Empty status string with nil error must still resolve to "unknown" — a blank
	// SCM reply is not a positive signal and must never read as a registered state.
	empty := newTestManager(Options{ServiceStatus: func() (string, error) {
		return "", nil
	}})
	if status, err := empty.ServiceStatus(context.Background()); err != nil || status.Status != "unknown" {
		t.Fatalf("empty injector string should resolve to unknown: status=%#v err=%v", status, err)
	}

	none := newTestManager(Options{})
	if status, err := none.ServiceStatus(context.Background()); err != nil || status.Status != "unknown" {
		t.Fatalf("nil injector err=%v status=%#v", err, status)
	}
}

type fakeInstaller struct {
	calls         atomic.Int64
	detectCalls   atomic.Int64
	candidate     PreparedCore
	prepare       func(context.Context, core.InstallRequest) (PreparedCore, error)
	detectVersion func(context.Context, string) (string, error)
}

func (i *fakeInstaller) Prepare(ctx context.Context, request core.InstallRequest) (PreparedCore, error) {
	i.calls.Add(1)
	if i.prepare != nil {
		return i.prepare(ctx, request)
	}
	return i.candidate, nil
}

func (i *fakeInstaller) DetectVersion(ctx context.Context, binaryPath string) (string, error) {
	i.detectCalls.Add(1)
	if i.detectVersion != nil {
		return i.detectVersion(ctx, binaryPath)
	}
	return "", nil
}

type fakeCandidate struct {
	version    string
	alphaSHA   string
	notUpdated bool
	commit     func() (core.InstallResult, error)
	cleaned    atomic.Bool
}

func (c *fakeCandidate) Version() string { return c.version }

func (c *fakeCandidate) Updated() bool { return !c.notUpdated }

func (c *fakeCandidate) Commit() (core.InstallResult, error) {
	if c.commit != nil {
		return c.commit()
	}
	return core.InstallResult{Version: c.version, Updated: !c.notUpdated, AlphaSHA: c.alphaSHA}, nil
}

func (c *fakeCandidate) Cleanup() { c.cleaned.Store(true) }

type fakeSupervisor struct {
	run     func(context.Context) error
	restart func(context.Context) error
}

func (s *fakeSupervisor) Run(ctx context.Context) error {
	if s.run != nil {
		return s.run(ctx)
	}
	<-ctx.Done()
	return nil
}

func (s *fakeSupervisor) Restart(ctx context.Context) error {
	if s.restart != nil {
		return s.restart(ctx)
	}
	return nil
}

type fakeController struct {
	mu                     sync.Mutex
	calls                  []controllerCall
	entered                chan<- struct{}
	release                <-chan struct{}
	selectProxy            func(context.Context, string, string) error
	selectProxyErr         error
	closeConnectionErr     error
	closeAllConnectionsErr error
	afterCall              func()
	updateRuleProvider     func(context.Context, string) error
	configs                map[string]any
	configsFunc            func(context.Context) (map[string]any, error)
	configsErr             error
	patchConfigs           func(context.Context, map[string]any) error
	lastPatch              map[string]any
	patchCalls             int
	reloads                int
	reloadErr              error
}

func (c *fakeController) Reload(context.Context, string, bool) error {
	c.reloads++
	return c.reloadErr
}

type controllerCall struct {
	kind string
	ctx  context.Context
	args []string
}

func (c *fakeController) recordCall(kind string, ctx context.Context, args ...string) {
	c.mu.Lock()
	c.calls = append(c.calls, controllerCall{kind: kind, ctx: ctx, args: append([]string(nil), args...)})
	c.mu.Unlock()
	if c.entered != nil {
		c.entered <- struct{}{}
	}
	if c.release != nil {
		<-c.release
	}
	if c.afterCall != nil {
		c.afterCall()
	}
}

func (c *fakeController) callsFor(kind string) []controllerCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	var calls []controllerCall
	for _, call := range c.calls {
		if call.kind == kind {
			calls = append(calls, call)
		}
	}
	return calls
}

func (c *fakeController) Proxies(context.Context) (mihomo.Proxies, error) {
	return mihomo.Proxies{}, nil
}

func (c *fakeController) SelectProxy(ctx context.Context, group, name string) error {
	c.recordCall("select", ctx, group, name)
	if c.selectProxy != nil {
		return c.selectProxy(ctx, group, name)
	}
	return c.selectProxyErr
}

func (c *fakeController) DelayGroup(context.Context, string, string, int) (mihomo.Delays, error) {
	return nil, nil
}

func (c *fakeController) DelayProxy(context.Context, string, string, int) (uint16, error) {
	return 0, nil
}

func (c *fakeController) Connections(context.Context) (mihomo.Connections, error) {
	return mihomo.Connections{}, nil
}

func (c *fakeController) CloseConnection(ctx context.Context, id string) error {
	c.recordCall("close", ctx, id)
	return c.closeConnectionErr
}

func (c *fakeController) CloseAllConnections(ctx context.Context) error {
	c.recordCall("close-all", ctx)
	return c.closeAllConnectionsErr
}

func (c *fakeController) Rules(context.Context) (mihomo.Rules, error) {
	return mihomo.Rules{}, nil
}

func (c *fakeController) RuleProviders(context.Context) (mihomo.RuleProviders, error) {
	return mihomo.RuleProviders{}, nil
}

func (c *fakeController) UpdateRuleProvider(ctx context.Context, name string) error {
	if c.updateRuleProvider != nil {
		return c.updateRuleProvider(ctx, name)
	}
	return nil
}

func (c *fakeController) Configs(ctx context.Context) (map[string]any, error) {
	if c.configsFunc != nil {
		return c.configsFunc(ctx)
	}
	if c.configsErr != nil {
		return nil, c.configsErr
	}
	if c.configs != nil {
		return c.configs, nil
	}
	return map[string]any{}, nil
}

func (c *fakeController) PatchConfigs(ctx context.Context, patch map[string]any) error {
	c.patchCalls++
	c.lastPatch = patch
	if c.patchConfigs != nil {
		return c.patchConfigs(ctx, patch)
	}
	if c.configs != nil {
		if tun, ok := patch["tun"].(map[string]any); ok {
			c.configs["tun"] = cloneTunMap(tun)
		}
	}
	return nil
}

func (c *fakeController) Stream(context.Context, mihomo.StreamKind, func(json.RawMessage) error) error {
	return nil
}

func requireCoreStatus(t *testing.T, store *state.Store, want string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if got := store.Load().Core.Status; got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("core status=%q want=%q", store.Load().Core.Status, want)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func waitManager(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("manager did not stop")
		return nil
	}
}

func TestManagerInstallSetupSkipsNetworkWhenLocalCoreValid(t *testing.T) {
	installer := &fakeInstaller{}
	installer.detectVersion = func(context.Context, string) (string, error) {
		return "v1.18.0", nil
	}
	installer.prepare = func(context.Context, core.InstallRequest) (PreparedCore, error) {
		t.Fatal("Prepare must not be called when setup local core is valid")
		return nil, nil
	}
	manager := newTestManager(Options{Installer: installer})
	manager.installRequest.BinaryPath = "mihomo"

	result, err := manager.Install(context.Background(), Operation{ID: "setup-valid", Source: "setup"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v1.18.0" || result.Updated {
		t.Fatalf("result=%#v", result)
	}
	if got := installer.calls.Load(); got != 0 {
		t.Fatalf("prepare calls=%d (setup must skip network)", got)
	}
	if got := installer.detectCalls.Load(); got != 1 {
		t.Fatalf("detect calls=%d", got)
	}
}

func TestManagerInstallSetupAppliesSidecarChannelWhenStampNew(t *testing.T) {
	installer := &fakeInstaller{}
	installer.detectVersion = func(context.Context, string) (string, error) {
		return "v1.18.0", nil
	}
	installer.prepare = func(context.Context, core.InstallRequest) (PreparedCore, error) {
		t.Fatal("Prepare must not be called when setup local core is valid")
		return nil, nil
	}

	binDir := t.TempDir()
	binaryPath := filepath.Join(binDir, "mihomo")
	if err := os.WriteFile(binaryPath, []byte("placeholder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "core-channel"), []byte("alpha\nalpha-e183c58\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	settings, settingsPath := persistedChannelSettings(t, "stable")
	manager := newTestManager(Options{Installer: installer, Settings: settings, SettingsPath: settingsPath})
	manager.installRequest.BinaryPath = binaryPath
	seeded := manager.store.Load()
	seeded.Core.Status = "running"
	seeded.Core.PID = 4242
	seeded.Core.Restarts = 3
	seeded.Core.AlphaSHA = "deadbeef"
	manager.store.Store(seeded)
	beforeRevision := manager.store.Load().Revision

	result, err := manager.Install(context.Background(), Operation{ID: "setup-sidecar", Source: "setup"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v1.18.0" || result.Updated {
		t.Fatalf("result=%#v", result)
	}
	if got := installer.calls.Load(); got != 0 {
		t.Fatalf("prepare calls=%d (setup must skip network)", got)
	}
	if manager.settings.CoreChannel != "alpha" || manager.settings.CoreChannelBundle != "alpha-e183c58" {
		t.Fatalf("settings channel=%q bundle=%q", manager.settings.CoreChannel, manager.settings.CoreChannelBundle)
	}
	core := manager.store.Load().Core
	if core.Channel != "alpha" {
		t.Fatalf("store.Core.Channel=%q want alpha", core.Channel)
	}
	if core.Version != "v1.18.0" {
		t.Fatalf("store.Core.Version=%q want v1.18.0", core.Version)
	}
	if core.Status != "running" || core.PID != 4242 || core.Restarts != 3 || core.AlphaSHA != "deadbeef" {
		t.Fatalf("setup sidecar store write must preserve existing Core fields: %#v", core)
	}
	if got := manager.store.Load().Revision; got != beforeRevision+1 {
		t.Fatalf("revision=%d want %d (coordinator.Do must increment)", got, beforeRevision+1)
	}
	loaded, err := config.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CoreChannel != "alpha" || loaded.CoreChannelBundle != "alpha-e183c58" {
		t.Fatalf("persisted settings=%#v", loaded)
	}
}

func TestManagerInstallSetupDoesNotRevertTUIChannelWhenSidecarStampMatches(t *testing.T) {
	installer := &fakeInstaller{}
	installer.detectVersion = func(context.Context, string) (string, error) {
		return "v1.18.0", nil
	}
	installer.prepare = func(context.Context, core.InstallRequest) (PreparedCore, error) {
		t.Fatal("Prepare must not be called when setup local core is valid")
		return nil, nil
	}

	binDir := t.TempDir()
	binaryPath := filepath.Join(binDir, "mihomo")
	if err := os.WriteFile(binaryPath, []byte("placeholder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "core-channel"), []byte("alpha\nalpha-e183c58\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	settings, settingsPath := persistedChannelSettings(t, "stable")
	settings.CoreChannelBundle = "alpha-e183c58"
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(Options{Installer: installer, Settings: settings, SettingsPath: settingsPath})
	manager.installRequest.BinaryPath = binaryPath

	if _, err := manager.Install(context.Background(), Operation{ID: "setup-sidecar-stamp", Source: "setup"}); err != nil {
		t.Fatal(err)
	}
	if manager.settings.CoreChannel != "stable" {
		t.Fatalf("settings.CoreChannel=%q want stable", manager.settings.CoreChannel)
	}
	if got := manager.store.Load().Core.Channel; got == "alpha" {
		t.Fatalf("store.Core.Channel=%q must not revert to alpha", got)
	}
	loaded, err := config.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CoreChannel != "stable" || loaded.CoreChannelBundle != "alpha-e183c58" {
		t.Fatalf("persisted settings=%#v", loaded)
	}
}

func TestManagerInstallSetupFallsBackToPrepareWhenLocalCoreInvalid(t *testing.T) {
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0"}}
	installer.detectVersion = func(context.Context, string) (string, error) {
		return "", errors.New("binary missing or corrupt")
	}
	prepareCalled := false
	installer.prepare = func(_ context.Context, _ core.InstallRequest) (PreparedCore, error) {
		prepareCalled = true
		return installer.candidate, nil
	}
	manager := newTestManager(Options{Installer: installer, Supervisor: &fakeSupervisor{}})
	manager.installRequest.BinaryPath = "mihomo"

	result, err := manager.Install(context.Background(), Operation{ID: "setup-invalid", Source: "setup"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "v1.19.0" {
		t.Fatalf("result=%#v", result)
	}
	if !prepareCalled {
		t.Fatal("Prepare must run when local core is invalid")
	}
	if got := installer.detectCalls.Load(); got != 1 {
		t.Fatalf("detect calls=%d (DetectVersion must run before fallback)", got)
	}
}

func TestManagerInstallControlDoesNotShortCircuit(t *testing.T) {
	installer := &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0", notUpdated: true}}
	installer.detectVersion = func(context.Context, string) (string, error) { return "v1.18.0", nil }
	manager := newTestManager(Options{Installer: installer, Supervisor: &fakeSupervisor{}})
	manager.installRequest.BinaryPath = "mihomo"

	if _, err := manager.Install(context.Background(), Operation{ID: "control-install", Source: "control"}); err != nil {
		t.Fatal(err)
	}
	if got := installer.detectCalls.Load(); got != 0 {
		t.Fatalf("detect calls=%d (must be 0 for control source)", got)
	}
	if got := installer.calls.Load(); got != 1 {
		t.Fatalf("prepare calls=%d (must be 1 for control source)", got)
	}
}

func TestUpdateGeoIPSetupSkipsNetworkWhenMMDBValid(t *testing.T) {
	service := &fakeGeoIPService{} // default: Country+ASN Available
	var prepareCalls atomic.Int64
	manager := newTestManager(Options{
		GeoIP: service,
		PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) {
			prepareCalls.Add(1)
			return nil, errors.New("prepareGeoIP must not be called when setup MMDB is valid")
		},
	})
	status, err := manager.UpdateGeoIP(context.Background(), Operation{ID: "setup-valid", Source: "setup"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Country.Available || !status.ASN.Available {
		t.Fatalf("status=%#v", status)
	}
	if got := prepareCalls.Load(); got != 0 {
		t.Fatalf("prepareGeoIP calls=%d (setup must skip network)", got)
	}
}

func TestUpdateGeoIPSetupFallsBackToNetworkWhenMMDBInvalid(t *testing.T) {
	service := &fakeGeoIPService{statusFunc: func() geoip.Status {
		return geoip.Status{Country: geoip.DatabaseStatus{Available: true}, ASN: geoip.DatabaseStatus{Available: false}}
	}}
	prepared := &fakeGeoIPCandidate{valid: true, identity: "fresh-pair"}
	var prepareCalls atomic.Int64
	manager := newTestManager(Options{
		GeoIP: service,
		PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) {
			prepareCalls.Add(1)
			return prepared, nil
		},
	})
	if _, err := manager.UpdateGeoIP(context.Background(), Operation{ID: "setup-invalid", Source: "setup"}); err != nil {
		t.Fatal(err)
	}
	if got := prepareCalls.Load(); got != 1 {
		t.Fatalf("prepareGeoIP calls=%d (must download when MMDB invalid)", got)
	}
}

func TestUpdateGeoIPControlDoesNotShortCircuit(t *testing.T) {
	service := &fakeGeoIPService{} // both Available — would short-circuit if source were setup
	prepared := &fakeGeoIPCandidate{valid: true, identity: "control-pair"}
	var prepareCalls atomic.Int64
	manager := newTestManager(Options{
		GeoIP: service,
		PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) {
			prepareCalls.Add(1)
			return prepared, nil
		},
	})
	if _, err := manager.UpdateGeoIP(context.Background(), Operation{ID: "control-geoip", Source: "control"}); err != nil {
		t.Fatal(err)
	}
	if got := prepareCalls.Load(); got != 1 {
		t.Fatalf("prepareGeoIP calls=%d (control must always download)", got)
	}
}
