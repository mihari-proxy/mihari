package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestDaemonAssembly_StartupCleanupIsDeferredAndCombinesOwners(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	binaryCause, coreCause := errors.New("binary cleanup"), errors.New("core cleanup")
	binaryCalls, coreCalls := 0, 0
	buildDaemonRuntime = func(platform.Paths, config.Settings, string, io.Writer, io.Writer, app.RuntimeBuildOptions) (*app.RuntimeAssembly, error) {
		return &app.RuntimeAssembly{StartupCleanup: func(context.Context) error { coreCalls++; return coreCause }}, nil
	}
	runDaemon = func(ctx context.Context, options daemon.Options) error {
		if binaryCalls != 0 || coreCalls != 0 {
			t.Fatal("cleanup ran before daemon took ownership")
		}
		if options.StartupCleanup == nil {
			t.Fatal("startup callback missing")
		}
		err := options.StartupCleanup(ctx)
		if !errors.Is(err, binaryCause) || !errors.Is(err, coreCause) || binaryCalls != 1 || coreCalls != 1 {
			t.Fatalf("cleanup: binary=%d core=%d err=%v", binaryCalls, coreCalls, err)
		}
		return nil
	}
	if err := runDaemonWith(t.Context(), daemonRunDeps{Paths: paths, PrivateFS: fs, BinaryCleanup: func(context.Context) error { binaryCalls++; return binaryCause }}); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonAssembly_ValidationHasNoStartupCleanup(t *testing.T) {
	if fn := daemonStartupCleanup(daemonRunDeps{ValidationMode: true}, func(context.Context) error { t.Fatal("unexpected cleanup"); return nil }); fn != nil {
		t.Fatal("validation received cleanup callback")
	}
}

func TestDaemonAssembly_DegradedStartupKeepsBinaryCleanup(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	called := false
	runDaemon = func(ctx context.Context, opts daemon.Options) error {
		if opts.StartupCleanup == nil {
			t.Fatal("degraded startup lost binary cleanup")
		}
		return opts.StartupCleanup(ctx)
	}
	err := runDegradedDaemon(t.Context(), daemonRunDeps{BinaryCleanup: func(context.Context) error { called = true; return nil }}, errors.New("runtime unavailable"), nil, nil, nil)
	if err != nil || !called {
		t.Fatalf("cleanup called=%v err=%v", called, err)
	}
}
