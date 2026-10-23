//go:build windows

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestClaimedListener_WindowsPipeExclusiveBeforeSettings(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := transporttest.Endpoint(t)
	loads := 0
	runDaemon = func(ctx context.Context, options daemon.Options) error {
		listener, err := options.Listen(ctx)
		if err != nil {
			return err
		}
		return listener.Close()
	}
	err = runDaemonWithClaimedListener(t.Context(), daemonRunDeps{
		Paths: paths, PrivateFS: fs, Endpoint: endpoint,
		LoadSettings: func(string, string) (config.Settings, bool, config.CommitResult, error) {
			loads++
			other, err := transport.Listen(endpoint)
			if err == nil {
				_ = other.Close() // Close the unexpected second test listener before failing.
				t.Fatal("another daemon could claim the pipe during business assembly")
			}
			return config.Settings{}, false, config.CommitResult{}, errors.New("synthetic invalid settings")
		},
	})
	if err != nil || loads != 1 {
		t.Fatalf("startup: loads=%d err=%v", loads, err)
	}
	// Both the serving path and the outer owner close the wrapper, exactly once.
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatalf("pipe ownership leaked: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
