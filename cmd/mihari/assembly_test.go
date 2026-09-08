package main

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/platform"
	"io"
	"net"
	"testing"
)

func TestDaemonAssembly_BusinessFailureRetainsOwnedListener(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	runDaemon = func(_ context.Context, opts daemon.Options) error {
		calls++
		if opts.Listen == nil {
			t.Error("degraded control lost owned listener")
		}
		return nil
	}
	err = runDaemonWith(context.Background(), daemonRunDeps{Paths: paths, PrivateFS: fs, Listen: func(context.Context) (net.Listener, error) { return nil, errors.New("owned") }, LoadSettings: func(string, string) (config.Settings, bool, config.CommitResult, error) {
		return config.Settings{}, false, config.CommitResult{}, errors.New("invalid business settings")
	}})
	if err != nil || calls != 1 {
		t.Fatalf("business configuration failure did not retain degraded control: calls=%d err=%v", calls, err)
	}
}

func TestDaemonAssembly_MachineSnapshotUsesActualLogging(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	buildDaemonRuntime = func(platform.Paths, config.Settings, string, io.Writer, io.Writer, app.RuntimeBuildOptions) (*app.RuntimeAssembly, error) {
		return nil, errors.New("invalid config")
	}
	runDaemon = func(_ context.Context, opts daemon.Options) error {
		if opts.SnapshotSource == nil {
			t.Error("system snapshot is not wired to actual logging owners")
		}
		if opts.Listen == nil {
			t.Error("degraded owned listener lost")
		}
		return nil
	}
	if err := runDaemonWith(context.Background(), daemonRunDeps{Paths: paths, PrivateFS: fs, MachineSnapshot: true, Listen: func(context.Context) (net.Listener, error) { return nil, errors.New("owned") }}); err != nil {
		t.Fatal(err)
	}
}
