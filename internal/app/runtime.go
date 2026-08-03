package app

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/core"
	"github.com/LeeShunEE/mihari/internal/geoip"
	"github.com/LeeShunEE/mihari/internal/mihomo"
	"github.com/LeeShunEE/mihari/internal/platform"
	"github.com/LeeShunEE/mihari/internal/preferences"
	runtimeapi "github.com/LeeShunEE/mihari/internal/runtime"
	"github.com/LeeShunEE/mihari/internal/state"
	"github.com/LeeShunEE/mihari/internal/subscription"
	"github.com/LeeShunEE/mihari/internal/supervisor"
)

type RuntimeAssembly struct {
	Manager *runtimeapi.Manager
	Store   *state.Store
}

func BuildRuntime(paths platform.Paths, settings config.Settings, daemonVersion string, stdout, stderr io.Writer) (*RuntimeAssembly, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}
	if err := core.EnsureRuntimeConfig(paths.RuntimeConfig, settings); err != nil {
		return nil, err
	}
	for _, endpoint := range []struct{ setting, address string }{
		{"mixed-addr", settings.MixedAddr},
		{"controller-addr", settings.ControllerAddr},
		{"web-addr", settings.WebAddr},
	} {
		listener, err := net.Listen("tcp", endpoint.address)
		if err != nil {
			return nil, protocol.APIError{
				Code: protocol.CodeInvalidState, Message: "managed port is unavailable",
				Details: map[string]any{"setting": endpoint.setting, "address": endpoint.address},
			}
		}
		_ = listener.Close()
	}

	store := state.NewStore(state.Snapshot{
		Version:   daemonVersion,
		StartedAt: time.Now().UTC(),
		Health:    "ok",
	})
	if info, err := os.Stat(paths.CoreBinary); err == nil && !info.IsDir() {
		if version, err := core.DetectVersion(context.Background(), core.OSCommandRunner{}, paths.CoreBinary); err == nil {
			snapshot := store.Load()
			snapshot.Core = state.CoreState{Status: "stopped", Version: version}
			store.Store(snapshot)
		}
	}
	coordinator := state.NewCoordinator(store)
	controller := mihomo.NewClient("http://"+settings.ControllerAddr, settings.ControllerSecret, nil)
	subscriptions, err := subscription.Open(subscription.ServiceOptions{
		CatalogPath: paths.SubscriptionCatalog,
		CacheDir:    paths.SubscriptionCache,
	})
	if err != nil {
		return nil, err
	}
	tuiPreferences, err := preferences.Open(paths.TUIPreferences)
	if err != nil {
		return nil, err
	}
	geoIPService := geoip.New(geoip.ServiceOptions{
		CountryPath: paths.GeoIPCountry,
		ASNPath:     paths.GeoIPASN,
		Downloader:  geoip.Downloader{StagingDir: paths.GeoIPStaging},
	})
	var manager *runtimeapi.Manager
	coreSupervisor := supervisor.New(supervisor.Options{
		Starter: supervisor.CommandStarter{
			BinaryPath: paths.CoreBinary,
			DataDir:    paths.Root,
			ConfigPath: paths.RuntimeConfig,
			Stdout:     stdout,
			Stderr:     stderr,
		},
		Health: func(ctx context.Context) error {
			_, err := controller.Version(ctx)
			return err
		},
		Observe: func(observation supervisor.Observation) {
			if manager != nil {
				manager.Observe(observation)
			}
		},
	})
	manager = runtimeapi.New(runtimeapi.Options{
		Store:       store,
		Coordinator: coordinator,
		Installer:   core.Installer{},
		InstallRequest: core.InstallRequest{
			BinaryPath: paths.CoreBinary,
			DataDir:    paths.Root,
			ConfigPath: paths.RuntimeConfig,
			StagingDir: paths.Staging,
		},
		Supervisor:    coreSupervisor,
		Controller:    controller,
		Subscriptions: subscriptions,
		Preferences:   tuiPreferences,
		GeoIP:         geoIPService,
		PrepareGeoIP: func(ctx context.Context) (runtimeapi.GeoIPCandidate, error) {
			return geoIPService.PrepareUpdate(ctx)
		},
		Settings:      settings,
		RuntimeConfig: paths.RuntimeConfig,
		StagingDir:    paths.SubscriptionStaging,
		ValidateConfig: func(ctx context.Context, candidatePath string) error {
			return core.ValidateConfig(ctx, core.OSCommandRunner{}, paths.CoreBinary, paths.Root, candidatePath)
		},
		RunScheduler: func(ctx context.Context) error {
			var schedulers sync.WaitGroup
			schedulers.Add(2)
			go func() {
				defer schedulers.Done()
				scheduler := subscription.NewScheduler(subscription.SchedulerOptions{
					Snapshot: subscriptions.Snapshot,
					Refresh: func(refreshContext context.Context, id string) error {
						_, err := manager.RefreshSubscription(refreshContext, runtimeapi.Operation{
							ID: "scheduler-" + id + "-" + time.Now().UTC().Format("20060102T150405.000000000"), Source: "scheduler",
						}, id)
						return err
					},
				})
				_ = scheduler.Run(ctx)
			}()
			go func() {
				defer schedulers.Done()
				scheduler := geoip.Scheduler{
					NeedsUpdate: geoIPService.NeedsUpdate,
					Refresh: func(refreshContext context.Context) error {
						_, err := manager.UpdateGeoIP(refreshContext, runtimeapi.Operation{
							ID: "scheduler-geoip-" + time.Now().UTC().Format("20060102T150405.000000000"), Source: "scheduler",
						})
						return err
					},
				}
				_ = scheduler.Run(ctx)
			}()
			<-ctx.Done()
			schedulers.Wait()
			return nil
		},
		BinaryExists: func() bool {
			info, err := os.Stat(paths.CoreBinary)
			return err == nil && !info.IsDir()
		},
	})
	return &RuntimeAssembly{Manager: manager, Store: store}, nil
}
