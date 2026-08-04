package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/core"
	"github.com/LeeShunEE/mihari/internal/geoip"
	"github.com/LeeShunEE/mihari/internal/mihomo"
	"github.com/LeeShunEE/mihari/internal/onboarding"
	"github.com/LeeShunEE/mihari/internal/preferences"
	"github.com/LeeShunEE/mihari/internal/state"
	"github.com/LeeShunEE/mihari/internal/supervisor"
)

func TestUpdateOnboardingRejectsStaleRevisionBeforePersistingEndpoints(t *testing.T) {
	directory := t.TempDir()
	settingsPath := filepath.Join(directory, "settings.json")
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	if err := config.Save(settingsPath, settings); err != nil {
		t.Fatal(err)
	}
	service, err := onboarding.Open(onboarding.Options{
		StatePath: filepath.Join(directory, "onboarding.json"), SettingsPath: settingsPath,
		Settings: settings, InitialSetupRequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(Options{Onboarding: service})
	manager.store.Store(state.Snapshot{Revision: 3})
	webAddr := "127.0.0.1:9292"
	stale := uint64(2)
	_, err = manager.UpdateOnboarding(context.Background(), Operation{ID: "setup-stale", Source: "test", IfRevision: &stale}, onboarding.Update{WebAddr: &webAddr})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
		t.Fatalf("err=%v", err)
	}
	if got := service.Status().WebAddr; got != settings.WebAddr {
		t.Fatalf("stale update persisted web address=%q", got)
	}

	current := uint64(3)
	status, err := manager.UpdateOnboarding(context.Background(), Operation{ID: "setup-current", Source: "test", IfRevision: &current}, onboarding.Update{WebAddr: &webAddr})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status.WebAddr != webAddr || !status.Status.RestartRequired || status.Revision != 4 || manager.Snapshot().Revision != 4 {
		t.Fatalf("status=%#v revision=%d", status, manager.Snapshot().Revision)
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

type fakeGeoIPService struct{ recordedError bool }

func (*fakeGeoIPService) Status() geoip.Status {
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

func TestRuntimeMutationWaitsForRestart(t *testing.T) {
	restartEntered := make(chan struct{})
	releaseRestart := make(chan struct{})
	selectCalled := make(chan struct{})
	manager := newTestManager(Options{
		Supervisor: &fakeSupervisor{restart: func(context.Context) error {
			close(restartEntered)
			<-releaseRestart
			return nil
		}},
		Controller: &fakeController{selectProxy: func(context.Context, string, string) error {
			close(selectCalled)
			return nil
		}},
	})
	restartDone := make(chan error, 1)
	go func() { restartDone <- manager.Restart(context.Background(), Operation{ID: "restart", Source: "test"}) }()
	select {
	case <-restartEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not begin")
	}
	selectDone := make(chan error, 1)
	go func() {
		selectDone <- manager.SelectProxy(context.Background(), Operation{ID: "select", Source: "test"}, "GLOBAL", "DIRECT")
	}()
	select {
	case <-selectCalled:
		t.Fatal("proxy selection overlapped restart")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseRestart)
	if err := <-restartDone; err != nil {
		t.Fatal(err)
	}
	if err := <-selectDone; err != nil {
		t.Fatal(err)
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

func newTestManager(options Options) *Manager {
	store := state.NewStore(state.Snapshot{Health: "ok"})
	options.Store = store
	options.Coordinator = state.NewCoordinator(store)
	if options.BinaryExists == nil {
		options.BinaryExists = func() bool { return true }
	}
	manager := New(options)
	manager.store = store
	return manager
}

type fakeInstaller struct {
	calls     atomic.Int64
	candidate PreparedCore
	prepare   func(context.Context, core.InstallRequest) (PreparedCore, error)
}

func (i *fakeInstaller) Prepare(ctx context.Context, request core.InstallRequest) (PreparedCore, error) {
	i.calls.Add(1)
	if i.prepare != nil {
		return i.prepare(ctx, request)
	}
	return i.candidate, nil
}

type fakeCandidate struct {
	version    string
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
	return core.InstallResult{Version: c.version, Updated: !c.notUpdated}, nil
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
	selectProxy        func(context.Context, string, string) error
	updateRuleProvider func(context.Context, string) error
}

func (c *fakeController) Proxies(context.Context) (mihomo.Proxies, error) {
	return mihomo.Proxies{}, nil
}

func (c *fakeController) SelectProxy(ctx context.Context, group, name string) error {
	if c.selectProxy != nil {
		return c.selectProxy(ctx, group, name)
	}
	return nil
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

func (c *fakeController) CloseConnection(context.Context, string) error { return nil }

func (c *fakeController) CloseAllConnections(context.Context) error { return nil }

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
