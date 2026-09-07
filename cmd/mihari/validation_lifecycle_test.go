package main

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

func TestActivation_CmdPendingBeforeBusinessIO(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	runDaemon = func(context.Context, daemon.Options) error { return nil }
	err = runDaemonWith(context.Background(), daemonRunDeps{Paths: paths, PrivateFS: fs, ActivationPhase: app.InstallPhasePrepared})
	if err == nil {
		t.Fatal("pending daemon accepted")
	}
	if _, err := os.Stat(paths.Settings); !os.IsNotExist(err) {
		t.Fatalf("pending daemon wrote settings: %v", err)
	}
}

func TestInstallValidation_CmdReadOnlyReadyAndCorruption(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "setup-required", true: "corrupt-catalog"}[corrupt], func(t *testing.T) {
			resetDaemonRunSeamsForTest(t)
			paths := absoluteTempPaths(t)
			fs, err := platform.NewPrivateFS(paths.Root)
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				if err := os.MkdirAll(filepath.Dir(paths.SubscriptionCatalog), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths.SubscriptionCatalog, []byte("schema: bad\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reached, setup := false, false
			done := errors.New("stop after ready")
			runDaemon = func(_ context.Context, opts daemon.Options) error {
				reached = true
				if opts.OnReady == nil {
					t.Fatal("missing private ready")
				}
				return opts.OnReady()
			}
			err = runDaemonWith(context.Background(), daemonRunDeps{Paths: paths, PrivateFS: fs, ValidationMode: true, ValidationReady: func(required bool) error { setup = required; return done }})
			if corrupt {
				if err == nil || reached {
					t.Fatalf("corruption became ready/degraded: %v ready=%v", err, reached)
				}
			} else if !errors.Is(err, done) || !reached || !setup {
				t.Fatalf("setup ready missing: %v ready=%v setup=%v", err, reached, setup)
			}
			for _, name := range []string{paths.Settings, paths.Onboarding, paths.WebCredential, paths.RuntimeConfig} {
				if _, err := os.Stat(name); !os.IsNotExist(err) {
					t.Fatalf("validation wrote business object %s: %v", filepath.Base(name), err)
				}
			}
		})
	}
}
