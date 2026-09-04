package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/logging"
)

func TestLoggingApplier_LatestWinsWithoutConcurrentApply(t *testing.T) {
	local := newBlockingLocalLogging()
	applier := newLoggingApplier(context.Background(), local)
	bootstrap := logging.BootstrapConfig()
	intermediate := loggingConfigForTest(t, "info", 10, 3)
	final := loggingConfigForTest(t, "error", 40, 7)

	if !applier.Submit(bootstrap) {
		t.Fatal("bootstrap Submit rejected")
	}
	if got := waitLoggingApply(t, local.started); got != bootstrap {
		t.Fatalf("first Apply=%+v want bootstrap %+v", got, bootstrap)
	}
	submitted := make(chan bool, 1)
	go func() { submitted <- applier.Submit(intermediate) && applier.Submit(final) }()
	select {
	case ok := <-submitted:
		if !ok {
			t.Fatal("effective Submit rejected")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Submit blocked behind Apply")
	}
	local.release <- struct{}{}
	if got, want := waitLoggingApply(t, local.started), final; got != want {
		t.Fatalf("second Apply=%+v want latest %+v", got, want)
	}
	local.release <- struct{}{}
	applier.CloseAndWait()

	local.mu.Lock()
	defer local.mu.Unlock()
	if local.maxActive != 1 {
		t.Fatalf("max concurrent Apply=%d want 1", local.maxActive)
	}
	if len(local.configs) != 2 {
		t.Fatalf("Apply configs=%+v want bootstrap and final only", local.configs)
	}
}

func TestLoggingApplier_CloseAndWaitCancelsApplyAndIsIdempotent(t *testing.T) {
	local := newBlockingLocalLogging()
	applier := newLoggingApplier(context.Background(), local)
	if !applier.Submit(logging.BootstrapConfig()) {
		t.Fatal("Submit rejected")
	}
	waitLoggingApply(t, local.started)

	done := make(chan struct{})
	go func() {
		applier.CloseAndWait()
		applier.CloseAndWait()
		close(done)
	}()
	select {
	case <-local.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("Apply did not receive cancellation")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("CloseAndWait did not join worker")
	}
	if applier.Submit(logging.DefaultConfig()) {
		t.Fatal("Submit succeeded after CloseAndWait")
	}
}

func TestLoggingApplier_NilRuntimeAcceptsUntilClosed(t *testing.T) {
	applier := newLoggingApplier(context.Background(), nil)
	if !applier.Submit(logging.BootstrapConfig()) {
		t.Fatal("nil-runtime Submit rejected")
	}
	applier.CloseAndWait()
	applier.CloseAndWait()
	if applier.Submit(logging.DefaultConfig()) {
		t.Fatal("closed nil-runtime applier accepted Submit")
	}
}

type blockingLocalLogging struct {
	mu         sync.Mutex
	active     int
	maxActive  int
	configs    []logging.Config
	started    chan logging.Config
	release    chan struct{}
	cancelled  chan struct{}
	cancelOnce sync.Once
}

func newBlockingLocalLogging() *blockingLocalLogging {
	return &blockingLocalLogging{
		started: make(chan logging.Config, 4), release: make(chan struct{}, 4), cancelled: make(chan struct{}),
	}
}

func (l *blockingLocalLogging) Apply(ctx context.Context, cfg logging.Config) {
	l.mu.Lock()
	l.active++
	l.maxActive = max(l.maxActive, l.active)
	l.configs = append(l.configs, cfg)
	l.mu.Unlock()
	l.started <- cfg
	select {
	case <-ctx.Done():
		l.cancelOnce.Do(func() { close(l.cancelled) })
	case <-l.release:
	}
	l.mu.Lock()
	l.active--
	l.mu.Unlock()
}

func waitLoggingApply(t *testing.T, started <-chan logging.Config) logging.Config {
	t.Helper()
	select {
	case cfg := <-started:
		return cfg
	case <-time.After(3 * time.Second):
		t.Fatal("logging Apply did not start")
		return logging.Config{}
	}
}

func loggingConfigForTest(t *testing.T, level string, maxSizeMB, maxFiles int64) logging.Config {
	t.Helper()
	cfg, err := logging.ConfigFromFields(level, maxSizeMB, maxFiles)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
