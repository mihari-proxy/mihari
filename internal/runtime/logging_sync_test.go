package runtime

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/supervisor"

	"github.com/mihari-proxy/mihari/internal/config"
)

func TestLoggingSync_AdoptsObservedCoreLevel(t *testing.T) {
	for _, level := range []string{"debug", "warning", "silent"} {
		t.Run(level, func(t *testing.T) {
			m, c := onlineLoggingManager(t)
			c.level = level
			if err := m.SyncLogging(t.Context()); err != nil {
				t.Fatal(err)
			}
			want := level
			if want == "warning" {
				want = "warn"
			}
			if m.settingsSnapshot().EffectiveLogging().Level != want {
				t.Fatalf("saved=%s", m.settingsSnapshot().EffectiveLogging().Level)
			}
			if c.patches != 0 || c.reloads != 0 {
				t.Fatal("adoption replayed core mutation")
			}
			revision := m.Snapshot().Revision
			if err := m.SyncLogging(t.Context()); err != nil {
				t.Fatal(err)
			}
			if m.Snapshot().Revision != revision {
				t.Fatal("unchanged observation advanced revision")
			}
		})
	}
}

func TestLoggingSync_PersistFailurePreservesCoreAndRetriesLatest(t *testing.T) {
	m, c := onlineLoggingManager(t)
	c.level = "debug"
	m.saveSettings = func(string, config.Settings) (config.CommitResult, error) {
		return config.CommitResult{}, errors.New("disk full")
	}
	if err := m.SyncLogging(t.Context()); err == nil {
		t.Fatal("save failure lost")
	}
	if c.level != "debug" || m.settingsSnapshot().EffectiveLogging().Level != "info" || c.patches != 0 {
		t.Fatal("failed adoption reverted core or published unsaved setting")
	}
	status, err := m.LoggingStatus(t.Context())
	if err != nil || status.SyncState != "unsaved" || status.CoreLevel != "debug" || status.Level != "info" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	candidate := configCandidate{content: []byte("log-level: info\n")}
	if err := m.commitRuntimeConfig(context.Background(), candidate); err == nil {
		t.Fatal("configuration reload overwrote unsaved external change")
	}
	if c.reloads != 0 {
		t.Fatal("unsaved guard ran after reload")
	}
	c.readErr = errors.New("controller temporarily unavailable")
	if err := m.SyncLogging(t.Context()); err == nil {
		t.Fatal("read failure lost")
	}
	if err := m.commitRuntimeConfig(t.Context(), candidate); err == nil {
		t.Fatal("unknown observation cleared unsaved protection")
	}
	c.readErr = nil
	c.level = "warning"
	m.saveSettings = config.SaveWithCommit
	if err := m.SyncLogging(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.settingsSnapshot().EffectiveLogging().Level != "warn" || c.level != "warning" {
		t.Fatal("retry did not adopt latest value")
	}
}

func TestLoggingSync_DiscardsObservationAcrossCoreEpoch(t *testing.T) {
	m, _ := onlineLoggingManager(t)
	entered, release := make(chan struct{}), make(chan struct{})
	m.controller = &fakeController{configsFunc: func(ctx context.Context) (map[string]any, error) {
		close(entered)
		select {
		case <-release:
			return map[string]any{"log-level": "debug"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	done := make(chan error, 1)
	go func() { done <- m.SyncLogging(t.Context()) }()
	<-entered
	m.Observe(supervisor.Observation{Status: supervisor.StatusStarting, PID: 42})
	m.Observe(supervisor.Observation{Status: supervisor.StatusRunning, PID: 42})
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if m.settingsSnapshot().EffectiveLogging().Level != "info" {
		t.Fatal("old instance result adopted after PID reuse")
	}
}

func TestLoggingSync_ManagerJoinsObserverWhenSupervisorReturns(t *testing.T) {
	m, _ := onlineLoggingManager(t)
	waiting, joined := make(chan struct{}), make(chan struct{})
	m.loggingWait = func(ctx context.Context, duration time.Duration) error {
		if duration != 2*time.Second {
			t.Errorf("interval=%s", duration)
		}
		close(waiting)
		<-ctx.Done()
		close(joined)
		return ctx.Err()
	}
	failure := errors.New("supervisor terminated")
	m.supervisor = &fakeSupervisor{run: func(context.Context) error { <-waiting; return failure }}
	if err := m.Run(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("Run=%v", err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("Run returned before observer joined")
	}
}

func TestLoggingSync_ActiveSubscriptionRemovalDoesNotReenterMutation(t *testing.T) {
	m, _, _, url := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	profile, err := m.AddSubscription(t.Context(), Operation{ID: "add"}, AddSubscriptionInput{Name: "fixture", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UseSubscription(t.Context(), Operation{ID: "use"}, profile.ID); err != nil {
		t.Fatal(err)
	}
	m.logging = &recordingLoggingRuntime{}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := m.RemoveSubscription(ctx, Operation{ID: "remove"}, profile.ID); err != nil {
		t.Fatal(err)
	}
}

func TestLoggingSync_UnsupportedObservationsDoNotChangeSavedValue(t *testing.T) {
	for _, value := range []any{"trace", "", 42, nil} {
		m, _ := onlineLoggingManager(t)
		m.controller = &fakeController{configs: map[string]any{"log-level": value}}
		if err := m.SyncLogging(t.Context()); err == nil {
			t.Fatalf("unsupported level accepted: %v", value)
		}
		status, err := m.LoggingStatus(t.Context())
		if err != nil || status.SyncState != "unknown" || status.CoreLevel != "" || status.Level != "info" {
			t.Fatalf("status=%+v err=%v", status, err)
		}
	}
}
