package main

import (
	"context"
	"io"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestDaemonAssembly_PropagatesExplicitProcessGroupMode(t *testing.T) {
	for _, share := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "foreground", true: "launchd"}[share]+"/"+map[bool]string{false: "options", true: "prepared"}[prepared], func(t *testing.T) {
				resetDaemonRunSeamsForTest(t)
				paths := absoluteTempPaths(t)
				fs, err := platform.NewPrivateFS(paths.Root)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				buildDaemonRuntime = func(_ platform.Paths, _ config.Settings, _ string, _, _ io.Writer, options app.RuntimeBuildOptions) (*app.RuntimeAssembly, error) {
					calls++
					if options.ShareProcessGroup != share {
						t.Errorf("shared group=%v want %v", options.ShareProcessGroup, share)
					}
					return &app.RuntimeAssembly{}, nil
				}
				runDaemon = func(context.Context, daemon.Options) error { return nil }
				deps := daemonRunDeps{Paths: paths, PrivateFS: fs, ShareProcessGroup: share, RuntimeOptions: app.RuntimeBuildOptions{ShareProcessGroup: !share}}
				if prepared {
					deps.PrepareRuntime = func(context.Context, config.Settings) (app.RuntimeBuildOptions, error) {
						return app.RuntimeBuildOptions{ShareProcessGroup: !share}, nil
					}
				}
				if err := runDaemonWith(context.Background(), deps); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("runtime builds=%d", calls)
				}
			})
		}
	}
}
