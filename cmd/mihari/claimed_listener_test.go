package main

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestClaimedListener_DuplicateStartupCannotLoadSettings(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	claimed := false
	loads := 0
	runDaemon = func(ctx context.Context, options daemon.Options) error {
		got, err := options.Listen(ctx)
		claimedListener, ok := got.(*claimedDaemonListener)
		if err != nil || !ok || claimedListener.Listener != listener {
			t.Fatalf("degraded startup lost claimed listener: %v", err)
		}
		return nil
	}
	err = runDaemonWithClaimedListener(t.Context(), daemonRunDeps{
		Paths: paths, PrivateFS: fs,
		Listen: func(context.Context) (net.Listener, error) { claimed = true; return listener, nil },
		LoadSettings: func(string, string) (config.Settings, bool, config.CommitResult, error) {
			loads++
			if !claimed {
				t.Error("settings loaded before exclusive listener claim")
			}
			duplicate := runDaemonWithClaimedListener(t.Context(), daemonRunDeps{
				Paths: paths, PrivateFS: fs,
				Listen: func(context.Context) (net.Listener, error) { return net.Listen("tcp", listener.Addr().String()) },
				LoadSettings: func(string, string) (config.Settings, bool, config.CommitResult, error) {
					t.Error("duplicate startup accessed business settings")
					return config.Settings{}, false, config.CommitResult{}, errors.New("duplicate loaded settings")
				},
			})
			if duplicate == nil {
				t.Error("duplicate startup accepted")
			}
			return config.Settings{}, false, config.CommitResult{}, errors.New("synthetic invalid settings")
		},
	})
	if err != nil || loads != 1 {
		t.Fatalf("startup: loads=%d err=%v", loads, err)
	}
	if err := listener.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener leaked: %v", err)
	}
}

func TestClaimedListener_CanceledStartupDoesNotClaim(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	err := runDaemonWithClaimedListener(ctx, daemonRunDeps{Listen: func(context.Context) (net.Listener, error) {
		calls++
		return nil, errors.New("unexpected listener claim")
	}})
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled startup claimed endpoint: calls=%d err=%v", calls, err)
	}
}

type cleanupFailureListener struct {
	net.Listener
	err   error
	calls int
}

func (l *cleanupFailureListener) Close() error {
	l.calls++
	return errors.Join(l.Listener.Close(), l.err)
}

func TestClaimedListener_DaemonOwnedCleanupDoesNotChangeResult(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	failure := errors.New("synthetic close failure")
	listener := &cleanupFailureListener{Listener: raw, err: failure}
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	runDaemon = func(ctx context.Context, options daemon.Options) error {
		owned, err := options.Listen(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// daemon.Run reports its own listener cleanup failure without changing
		// a successful serve result. The outer lifetime must preserve that.
		if err := owned.Close(); !errors.Is(err, failure) {
			t.Fatalf("close failure lost: %v", err)
		}
		return nil
	}
	err = runDaemonWithClaimedListener(t.Context(), daemonRunDeps{
		Paths: paths, PrivateFS: fs,
		Listen: func(context.Context) (net.Listener, error) { return listener, nil },
		LoadSettings: func(string, string) (config.Settings, bool, config.CommitResult, error) {
			return config.Settings{}, false, config.CommitResult{}, errors.New("synthetic invalid settings")
		},
	})
	if err != nil || listener.calls != 1 {
		t.Fatalf("daemon cleanup changed result: calls=%d err=%v", listener.calls, err)
	}
}

func TestClaimedListener_AssemblyFailureReleasesListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	err = runDaemonWithClaimedListener(t.Context(), daemonRunDeps{
		Listen: func(context.Context) (net.Listener, error) { return listener, nil },
	})
	if err == nil {
		t.Fatal("missing private filesystem accepted")
	}
	if err := listener.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener was not released: %v", err)
	}
}
