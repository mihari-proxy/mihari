package logging

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestRuntime_OpenCreatesBaseFile(t *testing.T) {
	fs, paths := openTestLogFS(t)
	rt := openTestRuntime(t, fs, paths.DaemonLog, "daemon", DefaultConfig())
	st, err := os.Stat(paths.DaemonLog)
	if err != nil {
		t.Fatalf("Open did not create base file: %v", err)
	}
	if st.IsDir() {
		t.Fatal("base path is a directory")
	}
	if rt.Logger() == nil {
		t.Fatal("Logger() is nil")
	}
	if rt.Config() != DefaultConfig() {
		t.Fatalf("Config()=%+v", rt.Config())
	}
}

func TestRuntime_OpenCancelAbortsLockWait(t *testing.T) {
	fs, paths := openTestLogFS(t)
	var spy *closeSpyLock
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Open(ctx, RuntimeOptions{
		BasePath:  paths.DaemonLog,
		Component: "daemon",
		Config:    DefaultConfig(),
		PrivateFS: fs,
		OpenLock: func(fs *platform.PrivateFS, path string) (platform.AdvisoryLock, error) {
			inner, err := platform.OpenAdvisoryLock(fs, path)
			if err != nil {
				return nil, err
			}
			spy = &closeSpyLock{AdvisoryLock: inner}
			return spy, nil
		},
		Redactor: NewRedactor(),
	})
	if err == nil {
		t.Fatal("expected Open to fail on canceled context")
	}
	if spy == nil {
		t.Fatal("OpenLock was not called")
	}
	if !spy.closed.Load() {
		t.Fatal("canceled Open left lock open")
	}
}

func TestRuntime_ApplySwapsConfigWithCanceledContext(t *testing.T) {
	fs, paths := openTestLogFS(t)
	cfg := Config{Level: slog.LevelInfo, MaxSizeBytes: 1000, MaxFiles: 3}
	rt := openTestRuntime(t, fs, paths.DaemonLog, "daemon", cfg)
	rt.Logger().Info("seed-record")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	next := Config{Level: slog.LevelError, MaxSizeBytes: 20, MaxFiles: 3}
	rt.Apply(ctx, next)
	if got := rt.Config(); got != next {
		t.Fatalf("Config()=%+v want %+v after canceled Apply", got, next)
	}
	if rt.Logger().Handler().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("LevelVar still enables INFO after swap to ERROR")
	}
	if !rt.Logger().Handler().Enabled(context.Background(), slog.LevelError) {
		t.Fatal("ERROR must stay enabled")
	}

	rt.Logger().Info("filtered")
	rt.Logger().Error(strings.Repeat("a", 40))
	if _, err := os.Stat(paths.DaemonLog + ".1"); err != nil {
		t.Fatalf("expected rotate under swapped max-size: %v", err)
	}
}

func TestRuntime_ApplyCleanupIgnoresCancelAfterLock(t *testing.T) {
	fs, paths := openTestLogFS(t)
	mustWriteFile(t, fs, paths.DaemonLog, []byte("active\n"))
	mustWriteFile(t, fs, paths.DaemonLog+".1", []byte("archive\n"))
	rt := openTestRuntime(t, fs, paths.DaemonLog, "daemon", Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3})

	ctx, cancel := context.WithCancel(context.Background())
	var removeSawCancel atomic.Bool
	t.Cleanup(func() {
		testAfterExclusiveLock = nil
		testBeforeRemove = nil
	})
	testAfterExclusiveLock = func() { cancel() }
	testBeforeRemove = func() {
		if ctx.Err() == nil {
			t.Error("Remove started before cancel")
			return
		}
		removeSawCancel.Store(true)
	}
	rt.Apply(ctx, Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 1})
	if _, err := os.Stat(paths.DaemonLog + ".1"); !os.IsNotExist(err) {
		t.Fatalf("cleanup should finish after cancel: %v", err)
	}
	if !removeSawCancel.Load() {
		t.Fatal("Remove did not observe the already-canceled context")
	}
	if got := readLogFile(t, paths.DaemonLog); string(got) != "active\n" {
		t.Fatalf("active rewritten during Apply: %q", got)
	}
}

func TestRuntime_CloseIdempotentDoesNotClosePrivateFS(t *testing.T) {
	fs, paths := openTestLogFS(t)
	rt, err := Open(context.Background(), RuntimeOptions{
		BasePath:  paths.DaemonLog,
		Component: "daemon",
		Config:    DefaultConfig(),
		PrivateFS: fs,
		Redactor:  NewRedactor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadDir(paths.LogDir); err != nil {
		t.Fatalf("Close closed shared PrivateFS: %v", err)
	}
	lock, err := platform.OpenAdvisoryLock(fs, paths.DaemonLog+".lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := lock.Lock(ctx, platform.LockExclusive); err != nil {
		t.Fatalf("Close left lock held: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntime_JSONComponent(t *testing.T) {
	fs, paths := openTestLogFS(t)
	for _, component := range []string{"daemon", "tui", "mihomo"} {
		t.Run(component, func(t *testing.T) {
			base := filepath.Join(paths.LogDir, "mihari-"+component+".log")
			rt := openTestRuntime(t, fs, base, component, DefaultConfig())
			rt.Logger().Info("hello")
			body := strings.TrimSpace(string(readLogFile(t, base)))
			var rec map[string]any
			if err := json.Unmarshal([]byte(body), &rec); err != nil {
				t.Fatalf("json: %v in %q", err, body)
			}
			if rec["component"] != component {
				t.Fatalf("component=%v want %s", rec["component"], component)
			}
			if rec["msg"] != "hello" {
				t.Fatalf("msg=%v", rec["msg"])
			}
			if _, ok := rec["source"]; ok {
				t.Fatalf("source present: %v", rec)
			}
		})
	}
}

func TestRuntime_EnterRecordMutex(t *testing.T) {
	fs, paths := openTestLogFS(t)
	rt := openTestRuntime(t, fs, paths.DaemonLog, "daemon", DefaultConfig())
	unlock := rt.EnterRecordMutex()
	unlock()
	unlock = rt.EnterRecordMutex()
	unlock()
}

func TestGroup_ApplySwapsAllTargetsBeforeConverge(t *testing.T) {
	fs, paths := openTestLogFS(t)
	old := Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3}
	mustWriteFile(t, fs, paths.DaemonLog, []byte("daemon-active\n"))
	mustWriteFile(t, fs, paths.DaemonLog+".1", []byte("daemon-archive\n"))
	mustWriteFile(t, fs, paths.MihomoLog, []byte("mihomo-active\n"))
	mustWriteFile(t, fs, paths.MihomoLog+".1", []byte("mihomo-archive\n"))

	blocked := make(chan struct{})
	first, err := Open(context.Background(), RuntimeOptions{
		BasePath:  paths.DaemonLog,
		Component: "daemon",
		Config:    old,
		PrivateFS: fs,
		OpenLock: func(fs *platform.PrivateFS, path string) (platform.AdvisoryLock, error) {
			inner, err := platform.OpenAdvisoryLock(fs, path)
			if err != nil {
				return nil, err
			}
			return &blockAfterOpenLock{AdvisoryLock: inner, blocked: blocked}, nil
		},
		Redactor: NewRedactor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second := openTestRuntime(t, fs, paths.MihomoLog, "mihomo", old)

	g := NewGroup(paths.LogDir, old, first, second)
	if g.Dir() != paths.LogDir {
		t.Fatalf("Dir()=%q", g.Dir())
	}
	if g.Config() != old {
		t.Fatalf("Config()=%+v", g.Config())
	}

	next := Config{Level: slog.LevelError, MaxSizeBytes: 512, MaxFiles: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.Apply(ctx, next)
	}()

	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("first target did not block on lock")
	}
	if got := second.Config(); got != next {
		t.Fatalf("second still has old config %+v while first lock blocked", got)
	}
	if got := first.Config(); got != next {
		t.Fatalf("first still has old config %+v while lock blocked", got)
	}
	if g.Config() != next {
		t.Fatalf("Group.Config()=%+v", g.Config())
	}
	if second.Logger().Handler().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("second LevelVar was not swapped before first converge")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Apply did not return after cancel")
	}
}

func TestGroup_ApplySerializesConcurrentCalls(t *testing.T) {
	fs, paths := openTestLogFS(t)
	initial := Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3}
	blocked := make(chan struct{})
	first, err := Open(context.Background(), RuntimeOptions{
		BasePath:  paths.DaemonLog,
		Component: "daemon",
		Config:    initial,
		PrivateFS: fs,
		OpenLock: func(fs *platform.PrivateFS, path string) (platform.AdvisoryLock, error) {
			inner, err := platform.OpenAdvisoryLock(fs, path)
			if err != nil {
				return nil, err
			}
			return &blockAfterOpenLock{AdvisoryLock: inner, blocked: blocked}, nil
		},
		Redactor: NewRedactor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second := openTestRuntime(t, fs, paths.MihomoLog, "mihomo", initial)
	g := NewGroup(paths.LogDir, initial, first, second)

	firstCfg := Config{Level: slog.LevelWarn, MaxSizeBytes: 512, MaxFiles: 2}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		g.Apply(firstCtx, firstCfg)
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("first Apply did not reach archive maintenance")
	}

	secondCfg := Config{Level: slog.LevelError, MaxSizeBytes: 256, MaxFiles: 1}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	cancelSecond()
	secondEntered := make(chan struct{})
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		g.apply(secondCtx, secondCfg, func() { close(secondEntered) })
	}()
	select {
	case <-secondEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("second Apply did not reach the serialization gate")
	}
	if got := g.Config(); got == secondCfg {
		t.Fatal("second Apply became observable while the first Apply was still running")
	}
	select {
	case <-secondDone:
		t.Fatal("second Apply overlapped the first Apply")
	default:
	}

	cancelFirst()
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first Apply did not return after cancel")
	}
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("queued Apply did not return")
	}
	if got := g.Config(); got != secondCfg {
		t.Fatalf("Group.Config()=%+v, want final %+v", got, secondCfg)
	}
	for name, target := range map[string]*Runtime{"daemon": first, "mihomo": second} {
		if got := target.Config(); got != secondCfg {
			t.Fatalf("%s config=%+v, want final %+v", name, got, secondCfg)
		}
	}
}

type blockAfterOpenLock struct {
	platform.AdvisoryLock
	armed   atomic.Bool
	blocked chan struct{}
}

func (l *blockAfterOpenLock) Lock(ctx context.Context, mode platform.LockMode) error {
	if l.armed.Load() {
		select {
		case <-l.blocked:
		default:
			close(l.blocked)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	return l.AdvisoryLock.Lock(ctx, mode)
}

func (l *blockAfterOpenLock) Unlock() error {
	err := l.AdvisoryLock.Unlock()
	l.armed.Store(true)
	return err
}

func openTestRuntime(t *testing.T, fs *platform.PrivateFS, base, component string, cfg Config) *Runtime {
	t.Helper()
	rt, err := Open(context.Background(), RuntimeOptions{
		BasePath:  base,
		Component: component,
		Config:    cfg,
		PrivateFS: fs,
		Redactor:  NewRedactor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}
