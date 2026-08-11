package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/panel/metacubexd"
	"github.com/mihari-proxy/mihari/internal/panel/zashboard"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/preferences"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"github.com/mihari-proxy/mihari/internal/web"
)

type RuntimeAssembly struct {
	Manager *runtimeapi.Manager
	Store   *state.Store
	Web     *web.Server
}

type RuntimeBuildOptions struct {
	InitialSetupRequired bool
	SettingsPath         string
}

func BuildRuntime(paths platform.Paths, settings config.Settings, daemonVersion string, stdout, stderr io.Writer) (*RuntimeAssembly, error) {
	return BuildRuntimeWithOptions(paths, settings, daemonVersion, stdout, stderr, RuntimeBuildOptions{SettingsPath: paths.Settings})
}

func BuildRuntimeWithOptions(paths platform.Paths, settings config.Settings, daemonVersion string, stdout, stderr io.Writer, options RuntimeBuildOptions) (*RuntimeAssembly, error) {
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
		ProxyAddr:   settings.MixedAddr,
	})
	if err != nil {
		return nil, err
	}
	// A persisted active subscription already had its generated config installed
	// into the runtime config file; without this the status API would report
	// "Not applied" after every daemon restart until the next subscription op.
	if catalog := subscriptions.Snapshot(); catalog.ActiveID != "" {
		if index := catalog.Index(catalog.ActiveID); index >= 0 && catalog.Profiles[index].Generation > 0 {
			snapshot := store.Load()
			snapshot.Config = state.ConfigState{Status: "ok", DesiredRevision: snapshot.Revision + 1, ObservedRevision: snapshot.Revision + 1}
			store.Store(snapshot)
		}
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
	settingsPath := options.SettingsPath
	if settingsPath == "" {
		settingsPath = paths.Settings
	}
	onboardingService, err := onboarding.Open(onboarding.Options{
		StatePath: paths.Onboarding, SettingsPath: settingsPath, Settings: settings, InitialSetupRequired: options.InitialSetupRequired,
	})
	if err != nil {
		return nil, err
	}
	webCredential, err := panel.LoadOrCreateCredential(paths.WebCredential)
	if err != nil {
		return nil, err
	}
	panelService, err := panel.Open(panel.ServiceOptions{
		WebRoot: paths.WebRoot, WebActive: paths.WebActive, StagingDir: paths.PanelStaging,
		Adapters: []panel.Adapter{
			zashboard.New(nil, ""),
			metacubexd.New(nil, ""),
		},
	})
	if err != nil {
		return nil, err
	}
	webGateway, err := web.New(web.Options{
		Addr: settings.WebAddr,
		Auth: web.Authenticator{
			WebCredential:    webCredential,
			ControllerSecret: settings.ControllerSecret,
		},
		ControllerURL:    "http://" + settings.ControllerAddr,
		ControllerSecret: settings.ControllerSecret,
		Panel:            panelService,
	})
	if err != nil {
		return nil, err
	}
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
		Onboarding:    onboardingService,
		Panels:        panelService,
		WebGateway:    webGateway,
		WebOpenToken:  webCredential,
		Settings:      settings,
		SettingsPath:  settingsPath,
		SysProxy:      sysproxy.Platform(),
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
	webGateway.Mutator = webMutator{manager: manager}
	return &RuntimeAssembly{Manager: manager, Store: store, Web: webGateway}, nil
}

// webMutator routes browser mutations through the daemon coordinator.
type webMutator struct {
	manager *runtimeapi.Manager
}

func (m webMutator) SelectProxy(ctx context.Context, group, name string) error {
	return m.manager.SelectProxy(ctx, runtimeapi.Operation{
		ID: "web-select-" + time.Now().UTC().Format("20060102T150405.000000000"), Source: "web",
	}, group, name)
}

func (m webMutator) CloseConnection(ctx context.Context, id string) error {
	return m.manager.CloseConnection(ctx, runtimeapi.Operation{
		ID: "web-close-" + time.Now().UTC().Format("20060102T150405.000000000"), Source: "web",
	}, id)
}

func (m webMutator) CloseAllConnections(ctx context.Context) error {
	return m.manager.CloseAllConnections(ctx, runtimeapi.Operation{
		ID: "web-close-all-" + time.Now().UTC().Format("20060102T150405.000000000"), Source: "web",
	})
}

// ApplyConfigPatch applies allowlisted config mutations (currently TUN only) via the coordinator.
func (m webMutator) ApplyConfigPatch(ctx context.Context, patch map[string]any) error {
	tunRaw, ok := patch["tun"]
	if !ok {
		return protocol.APIError{
			Code:    protocol.CodeUnsupportedMutation,
			Message: "unsupported config mutation",
		}
	}
	tun, ok := tunRaw.(map[string]any)
	if !ok {
		return protocol.APIError{
			Code:    protocol.CodeInvalidArgument,
			Message: "tun config must be an object",
		}
	}
	enable, ok := tun["enable"].(bool)
	if !ok {
		return protocol.APIError{
			Code:    protocol.CodeInvalidArgument,
			Message: "tun.enable must be a boolean",
		}
	}
	op := runtimeapi.Operation{ID: "web-tun-" + newWebOperationID(), Source: "web"}
	if enable {
		_, err := m.manager.EnableTun(ctx, op)
		return err
	}
	_, err := m.manager.DisableTun(ctx, op)
	return err
}

func newWebOperationID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		// Fallback keeps operation IDs unique enough for coordinator logging.
		return time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(value[:])
}
