package runtime

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

type recordingLoggingRuntime struct {
	mu         sync.Mutex
	cfg        logging.Config
	dir        string
	applyCalls int
	apply      func(context.Context, logging.Config)
}

func (r *recordingLoggingRuntime) Apply(ctx context.Context, cfg logging.Config) {
	if r.apply != nil {
		r.apply(ctx, cfg)
	}
	r.mu.Lock()
	r.cfg = cfg
	r.applyCalls++
	r.mu.Unlock()
}

func (r *recordingLoggingRuntime) Config() logging.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

func (r *recordingLoggingRuntime) Dir() string { return r.dir }

func TestLogging_NilRuntimeIsUnavailableAndNotAdvertised(t *testing.T) {
	manager := newTestManager(Options{})
	if slices.Contains(manager.Capabilities(), protocol.CapabilityLogging) {
		t.Fatalf("capabilities=%v", manager.Capabilities())
	}
	_, err := manager.LoggingStatus(context.Background())
	assertLoggingAPIError(t, err, protocol.CodeInvalidState)
	_, err = manager.UpdateLogging(context.Background(), Operation{ID: "nil-runtime", Source: "test"}, LoggingUpdate{Level: stringPointer("debug")})
	assertLoggingAPIError(t, err, protocol.CodeInvalidState)
}

func TestLogging_ConcurrentGETWaitsForSaveBeforePublish(t *testing.T) {
	saveEntered := make(chan struct{})
	releaseSave := make(chan struct{})
	runtime := &recordingLoggingRuntime{dir: "logs"}
	manager := newTestManager(Options{
		Settings: config.Defaults(), Logging: runtime, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			close(saveEntered)
			<-releaseSave
			return config.CommitResult{Committed: true}, nil
		},
	})
	updateDone := make(chan error, 1)
	go func() {
		_, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-blocked-save", Source: "test"}, LoggingUpdate{Level: stringPointer("debug")})
		updateDone <- err
	}()
	select {
	case <-saveEntered:
	case err := <-updateDone:
		t.Fatalf("update returned before Save: %v", err)
	case <-time.After(time.Second):
		t.Fatal("update did not reach Save")
	}

	readDone := make(chan protocol.LoggingStatus, 1)
	readErr := make(chan error, 1)
	go func() {
		status, err := manager.LoggingStatus(context.Background())
		readDone <- status
		readErr <- err
	}()
	waitForRuntimeStack(t, "(*Manager).LoggingStatus", "(*Manager).lockMaintenance")
	select {
	case status := <-readDone:
		t.Fatalf("GET observed mutation while Save was pending: %#v", status)
	default:
	}
	close(releaseSave)
	if err := <-updateDone; err != nil {
		t.Fatal(err)
	}
	if err := <-readErr; err != nil {
		t.Fatal(err)
	}
	if status := <-readDone; status.Level != "debug" || status.Revision != 1 {
		t.Fatalf("status=%#v", status)
	}
}

func TestLogging_StatusReturnsOneMaintenanceSnapshot(t *testing.T) {
	settings := config.Defaults()
	settings.SetLogging(config.LoggingSettings{Level: "debug", MaxSizeMB: 20, MaxFiles: 5})
	runtime := &recordingLoggingRuntime{dir: `C:\absolute\mihari\logs`}
	manager := newTestManager(Options{Settings: settings, Logging: runtime})
	manager.store.Store(state.Snapshot{Revision: 7, Health: "ok"})

	got, err := manager.LoggingStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.LoggingStatus{
		Schema: "mihari/v1", Revision: 7, Level: "debug", MaxSizeMB: 20, MaxFiles: 5, Dir: runtime.dir,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status=%#v want=%#v", got, want)
	}
}

func TestLogging_UpdateRejectsInvalidDomainValuesBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name      string
		operation Operation
		update    LoggingUpdate
	}{
		{name: "empty operation ID", operation: Operation{Source: "test"}, update: LoggingUpdate{Level: stringPointer("debug")}},
		{name: "empty update", operation: Operation{ID: "empty", Source: "test"}},
		{name: "empty level", operation: Operation{ID: "empty-level", Source: "test"}, update: LoggingUpdate{Level: stringPointer("")}},
		{name: "uppercase level", operation: Operation{ID: "uppercase", Source: "test"}, update: LoggingUpdate{Level: stringPointer("INFO")}},
		{name: "unknown level", operation: Operation{ID: "unknown", Source: "test"}, update: LoggingUpdate{Level: stringPointer("trace")}},
		{name: "zero size", operation: Operation{ID: "zero-size", Source: "test"}, update: LoggingUpdate{MaxSizeMB: int64Pointer(0)}},
		{name: "negative size", operation: Operation{ID: "negative-size", Source: "test"}, update: LoggingUpdate{MaxSizeMB: int64Pointer(-1)}},
		{name: "large size", operation: Operation{ID: "large-size", Source: "test"}, update: LoggingUpdate{MaxSizeMB: int64Pointer(101)}},
		{name: "zero files", operation: Operation{ID: "zero-files", Source: "test"}, update: LoggingUpdate{MaxFiles: int64Pointer(0)}},
		{name: "negative files", operation: Operation{ID: "negative-files", Source: "test"}, update: LoggingUpdate{MaxFiles: int64Pointer(-1)}},
		{name: "many files", operation: Operation{ID: "many-files", Source: "test"}, update: LoggingUpdate{MaxFiles: int64Pointer(11)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var saves atomic.Int64
			runtime := &recordingLoggingRuntime{}
			manager := newTestManager(Options{
				Settings: config.Defaults(), Logging: runtime, SettingsPath: "settings.yaml",
				SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
					saves.Add(1)
					return config.CommitResult{Committed: true}, nil
				},
			})
			_, err := manager.UpdateLogging(context.Background(), test.operation, test.update)
			assertLoggingAPIError(t, err, protocol.CodeInvalidArgument)
			if saves.Load() != 0 || runtime.applyCalls != 0 || manager.Snapshot().Revision != 0 {
				t.Fatalf("saves=%d apply=%d revision=%d", saves.Load(), runtime.applyCalls, manager.Snapshot().Revision)
			}
		})
	}
}

func TestLogging_UpdateCommitsSavePublishApplyRevisionInOrder(t *testing.T) {
	var manager *Manager
	var order []string
	runtime := &recordingLoggingRuntime{dir: `C:\absolute\mihari\logs`}
	runtime.apply = func(_ context.Context, cfg logging.Config) {
		order = append(order, "apply")
		if got := manager.settingsSnapshot().EffectiveLogging(); got != (config.LoggingSettings{Level: "debug", MaxSizeMB: 20, MaxFiles: 5}) {
			t.Fatalf("settings at Apply=%#v", got)
		}
		if got := manager.Snapshot().Revision; got != 0 {
			t.Fatalf("revision advanced before Apply: %d", got)
		}
		if cfg != (logging.Config{Level: slog.LevelDebug, MaxSizeBytes: 20 << 20, MaxFiles: 5}) {
			t.Fatalf("applied config=%#v", cfg)
		}
	}
	manager = newTestManager(Options{
		Settings: config.Defaults(), Logging: runtime, SettingsPath: "settings.yaml",
		SaveSettings: func(_ string, saved config.Settings) (config.CommitResult, error) {
			order = append(order, "save")
			if got := manager.settingsSnapshot().EffectiveLogging(); got != config.DefaultLoggingSettings() {
				t.Fatalf("settings published before Save returned: %#v", got)
			}
			if got := saved.EffectiveLogging(); got != (config.LoggingSettings{Level: "debug", MaxSizeMB: 20, MaxFiles: 5}) {
				t.Fatalf("saved settings=%#v", got)
			}
			return config.CommitResult{Committed: true, Warning: errors.New(`C:\sensitive\settings.yaml`)}, nil
		},
		OnBackgroundError: func(component string, err error) {
			order = append(order, "warning")
			if component != "settings" || err.Error() != "parent directory sync failed after commit" {
				t.Fatalf("warning component=%q err=%v", component, err)
			}
		},
	})

	got, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-ordered", Source: "test"}, LoggingUpdate{
		Level: stringPointer("debug"), MaxSizeMB: int64Pointer(20), MaxFiles: int64Pointer(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"save", "warning", "apply"}) {
		t.Fatalf("order=%v", order)
	}
	if got != (protocol.LoggingStatus{Schema: "mihari/v1", Revision: 1, Level: "debug", MaxSizeMB: 20, MaxFiles: 5, Dir: runtime.dir}) {
		t.Fatalf("status=%#v", got)
	}
	if manager.Snapshot().Revision != 1 || runtime.applyCalls != 1 {
		t.Fatalf("revision=%d apply=%d", manager.Snapshot().Revision, runtime.applyCalls)
	}
}

func TestLogging_UpdateRejectsStaleRevisionBeforeSave(t *testing.T) {
	var saves atomic.Int64
	runtime := &recordingLoggingRuntime{}
	manager := newTestManager(Options{
		Settings: config.Defaults(), Logging: runtime, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	manager.store.Store(state.Snapshot{Revision: 4, Health: "ok"})
	stale := uint64(3)
	_, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-stale", Source: "test", IfRevision: &stale}, LoggingUpdate{Level: stringPointer("debug")})
	assertLoggingAPIError(t, err, protocol.CodeRevisionConflict)
	if saves.Load() != 0 || runtime.applyCalls != 0 || manager.Snapshot().Revision != 4 {
		t.Fatalf("saves=%d apply=%d revision=%d", saves.Load(), runtime.applyCalls, manager.Snapshot().Revision)
	}
}

func TestLogging_UpdatePreCommitFailureLeavesEveryConsumerUnchanged(t *testing.T) {
	runtime := &recordingLoggingRuntime{cfg: logging.DefaultConfig()}
	manager := newTestManager(Options{
		Settings: config.Defaults(), Logging: runtime, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			return config.CommitResult{}, errors.New("replace failed")
		},
	})
	_, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-save-fail", Source: "test"}, LoggingUpdate{Level: stringPointer("debug")})
	assertLoggingAPIError(t, err, protocol.CodeDataFailure)
	if got := manager.settingsSnapshot().EffectiveLogging(); got != config.DefaultLoggingSettings() {
		t.Fatalf("settings=%#v", got)
	}
	if runtime.Config() != logging.DefaultConfig() || runtime.applyCalls != 0 || manager.Snapshot().Revision != 0 {
		t.Fatalf("runtime=%#v apply=%d revision=%d", runtime.Config(), runtime.applyCalls, manager.Snapshot().Revision)
	}
}

func TestLogging_NoOpSkipsSaveAndApplyButCommitsRevision(t *testing.T) {
	var saves atomic.Int64
	runtime := &recordingLoggingRuntime{cfg: logging.DefaultConfig(), dir: "logs"}
	settings := config.Defaults()
	settings.Logging = &config.LoggingSettings{Level: "info"}
	manager := newTestManager(Options{
		Settings: settings, Logging: runtime, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	got, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-noop", Source: "test"}, LoggingUpdate{Level: stringPointer("info")})
	if err != nil {
		t.Fatal(err)
	}
	if saves.Load() != 0 || runtime.applyCalls != 0 || got.Revision != 1 || manager.Snapshot().Revision != 1 {
		t.Fatalf("status=%#v saves=%d apply=%d revision=%d", got, saves.Load(), runtime.applyCalls, manager.Snapshot().Revision)
	}
}

func TestLogging_SameOperationIDReturnsCommittedResultWithoutRepeating(t *testing.T) {
	var saves atomic.Int64
	runtime := &recordingLoggingRuntime{dir: "logs"}
	manager := newTestManager(Options{
		Settings: config.Defaults(), Logging: runtime, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	op := Operation{ID: "logging-idempotent", Source: "test"}
	first, err := manager.UpdateLogging(context.Background(), op, LoggingUpdate{MaxFiles: int64Pointer(5)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.UpdateLogging(context.Background(), op, LoggingUpdate{MaxFiles: int64Pointer(5)})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || saves.Load() != 1 || runtime.applyCalls != 1 || manager.Snapshot().Revision != 1 {
		t.Fatalf("first=%#v second=%#v saves=%d apply=%d revision=%d", first, second, saves.Load(), runtime.applyCalls, manager.Snapshot().Revision)
	}
}

func TestLogging_DegradedManagerAllowsReadButRejectsWriteBeforeSave(t *testing.T) {
	var saves atomic.Int64
	runtime := &recordingLoggingRuntime{dir: "logs"}
	manager := newTestManager(Options{
		Settings: config.Defaults(), Logging: runtime, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves.Add(1)
			return config.CommitResult{Committed: true}, nil
		},
	})
	manager.mutationDegraded.Store(true)
	if status, err := manager.LoggingStatus(context.Background()); err != nil || status.Level != "info" {
		t.Fatalf("read status=%#v err=%v", status, err)
	}
	_, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-degraded", Source: "test"}, LoggingUpdate{Level: stringPointer("debug")})
	assertLoggingAPIError(t, err, protocol.CodeInvalidState)
	if saves.Load() != 0 || runtime.applyCalls != 0 || manager.Snapshot().Revision != 0 {
		t.Fatalf("saves=%d apply=%d revision=%d", saves.Load(), runtime.applyCalls, manager.Snapshot().Revision)
	}
}

func TestLogging_ConcurrentGETWaitsForCompleteCommittedMutation(t *testing.T) {
	applyEntered := make(chan struct{})
	releaseApply := make(chan struct{})
	runtime := &recordingLoggingRuntime{dir: "logs", apply: func(context.Context, logging.Config) {
		close(applyEntered)
		<-releaseApply
	}}
	manager := newTestManager(Options{
		Settings: config.Defaults(), Logging: runtime, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			return config.CommitResult{Committed: true}, nil
		},
	})
	updated := make(chan protocol.LoggingStatus, 1)
	updateErr := make(chan error, 1)
	go func() {
		status, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-blocked-apply", Source: "test"}, LoggingUpdate{Level: stringPointer("debug")})
		updated <- status
		updateErr <- err
	}()
	select {
	case <-applyEntered:
	case err := <-updateErr:
		t.Fatalf("update returned before Apply: %v", err)
	case <-time.After(time.Second):
		t.Fatal("update did not reach Apply")
	}

	read := make(chan protocol.LoggingStatus, 1)
	readErr := make(chan error, 1)
	go func() {
		status, err := manager.LoggingStatus(context.Background())
		read <- status
		readErr <- err
	}()
	waitForRuntimeStack(t, "(*Manager).LoggingStatus", "(*Manager).lockMaintenance")
	select {
	case status := <-read:
		t.Fatalf("GET observed in-progress mutation: %#v", status)
	default:
	}
	close(releaseApply)
	if err := <-updateErr; err != nil {
		t.Fatal(err)
	}
	if status := <-updated; status.Level != "debug" || status.Revision != 1 {
		t.Fatalf("updated=%#v", status)
	}
	if err := <-readErr; err != nil {
		t.Fatal(err)
	}
	if status := <-read; status.Level != "debug" || status.Revision != 1 {
		t.Fatalf("read=%#v", status)
	}
}

func TestLogging_CommittedSaveIgnoresLateCancellationAndCachesResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var saves atomic.Int64
	var applySawCancellation atomic.Bool
	runtime := &recordingLoggingRuntime{dir: "logs", apply: func(ctx context.Context, _ logging.Config) {
		applySawCancellation.Store(errors.Is(ctx.Err(), context.Canceled))
	}}
	manager := newTestManager(Options{
		Settings: config.Defaults(), Logging: runtime, SettingsPath: "settings.yaml",
		SaveSettings: func(string, config.Settings) (config.CommitResult, error) {
			saves.Add(1)
			cancel()
			return config.CommitResult{Committed: true}, nil
		},
	})
	op := Operation{ID: "logging-cancelled-after-commit", Source: "test"}
	first, err := manager.UpdateLogging(ctx, op, LoggingUpdate{MaxFiles: int64Pointer(5)})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := manager.UpdateLogging(context.Background(), op, LoggingUpdate{MaxFiles: int64Pointer(5)})
	if err != nil {
		t.Fatal(err)
	}
	if first != retry || first.Revision != 1 || saves.Load() != 1 || runtime.applyCalls != 1 || !applySawCancellation.Load() {
		t.Fatalf("first=%#v retry=%#v saves=%d apply=%d canceled=%v", first, retry, saves.Load(), runtime.applyCalls, applySawCancellation.Load())
	}
}

func TestManagerSettings_LoggingThenPorts(t *testing.T) {
	manager, settingsPath := loggingSequenceManager(t, true, nil)
	if _, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-first", Source: "test"}, LoggingUpdate{MaxFiles: int64Pointer(5)}); err != nil {
		t.Fatal(err)
	}
	webAddr := "127.0.0.1:9292"
	if _, err := manager.UpdateOnboarding(context.Background(), Operation{ID: "ports-second", Source: "test"}, onboarding.Update{WebAddr: &webAddr}); err != nil {
		t.Fatal(err)
	}
	assertLoggingAndSettingsFields(t, manager, settingsPath, config.LoggingSettings{Level: "info", MaxSizeMB: 10, MaxFiles: 5}, webAddr, false)
}

func TestManagerSettings_PortsThenLogging(t *testing.T) {
	manager, settingsPath := loggingSequenceManager(t, true, nil)
	webAddr := "127.0.0.1:9292"
	if _, err := manager.UpdateOnboarding(context.Background(), Operation{ID: "ports-first", Source: "test"}, onboarding.Update{WebAddr: &webAddr}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-second", Source: "test"}, LoggingUpdate{Level: stringPointer("debug")}); err != nil {
		t.Fatal(err)
	}
	assertLoggingAndSettingsFields(t, manager, settingsPath, config.LoggingSettings{Level: "debug", MaxSizeMB: 10, MaxFiles: 3}, webAddr, false)
}

func TestManagerSettings_LoggingThenSystemProxy(t *testing.T) {
	backend := &sysproxy.FakeBackend{}
	manager, settingsPath := loggingSequenceManager(t, false, backend)
	if _, err := manager.UpdateLogging(context.Background(), Operation{ID: "logging-first", Source: "test"}, LoggingUpdate{MaxFiles: int64Pointer(5)}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnableSystemProxy(context.Background(), Operation{ID: "sysproxy-second", Source: "test"}, false); err != nil {
		t.Fatal(err)
	}
	assertLoggingAndSettingsFields(t, manager, settingsPath, config.LoggingSettings{Level: "info", MaxSizeMB: 10, MaxFiles: 5}, config.Defaults().WebAddr, true)
}

func loggingSequenceManager(t *testing.T, withOnboarding bool, backend sysproxy.Backend) (*Manager, string) {
	t.Helper()
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	options := Options{
		Settings: settings, SettingsPath: filepath.Join(t.TempDir(), "mihari.yaml"),
		Logging: &recordingLoggingRuntime{dir: "logs"}, SysProxy: backend,
	}
	if withOnboarding {
		service, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json")})
		if err != nil {
			t.Fatal(err)
		}
		options.Onboarding = service
	}
	return newTestManager(options), options.SettingsPath
}

func assertLoggingAndSettingsFields(t *testing.T, manager *Manager, settingsPath string, wantLogging config.LoggingSettings, wantWebAddr string, wantSystemProxy bool) {
	t.Helper()
	loaded, err := config.Load(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, settings := range map[string]config.Settings{"memory": manager.settingsSnapshot(), "disk": loaded} {
		if settings.EffectiveLogging() != wantLogging || settings.WebAddr != wantWebAddr || settings.SystemProxyDesired != wantSystemProxy {
			t.Fatalf("%s settings=%#v", name, settings)
		}
	}
}

func assertLoggingAPIError(t *testing.T, err error, want protocol.ErrorCode) {
	t.Helper()
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != want {
		t.Fatalf("err=%v code=%q want=%q", err, apiError.Code, want)
	}
}

func stringPointer(value string) *string { return &value }

func int64Pointer(value int64) *int64 { return &value }
