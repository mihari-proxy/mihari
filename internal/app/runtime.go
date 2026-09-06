package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
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
	"github.com/mihari-proxy/mihari/internal/tundetect"
	"github.com/mihari-proxy/mihari/internal/web"
	"go.yaml.in/yaml/v3"
)

type RuntimeAssembly struct {
	Manager       *runtimeapi.Manager
	Store         *state.Store
	Web           *web.Server
	mihomoStarter supervisor.CommandStarter
}

type RuntimeBuildOptions struct {
	// TrustedCore and RootConfigInput are enabled together by the Unix assembly.
	// Default dispatch remains legacy until the final system-mode activation.
	TrustedCore     *core.TrustedExecution
	RootConfigInput func(context.Context, subscription.Document, config.Settings) (subscription.PolicyInput, error)
	Resources       StartupResources

	InitialSetupRequired bool
	SettingsPath         string
	ServiceStatus        func() (string, error)
	Logging              runtimeapi.LoggingRuntime
	RefreshLogSecrets    func(catalogURLs []string)
	MihomoStdout         io.Writer
	MihomoStderr         io.Writer
	SaveOnboardingState  func(string, onboarding.State) (config.CommitResult, error)
	OnBackgroundError    func(component string, err error)
}

// StartupResources recovers provider/resource WALs and reconstructs the
// authoritative offline resource graph before mihomo validation or start.
type StartupResources interface {
	Recover(context.Context) error
	SnapshotResources(context.Context, subscription.PolicyInput) (*subscription.ResourceGraph, error)
	OfflineInput(context.Context, subscription.PolicyInput, *subscription.ResourceGraph) (subscription.PolicyInput, error)
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
	if options.TrustedCore != nil {
		if err := options.TrustedCore.CheckPaths(paths.Root, paths.CoreBinary, paths.RuntimeConfig); err != nil {
			return nil, err
		}
	}
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}
	subscriptions, err := subscription.Open(subscription.ServiceOptions{
		CatalogPath: paths.SubscriptionCatalog,
		CacheDir:    paths.SubscriptionCache,
		ProxyAddr:   settings.MixedAddr,
	})
	if err != nil {
		return nil, err
	}
	installer := core.Installer{}
	if options.TrustedCore != nil {
		if options.RootConfigInput == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "root configuration context unavailable"}
		}
		if options.Resources != nil {
			if err := options.Resources.Recover(context.Background()); err != nil {
				return nil, err
			}
		}
		if err := core.RecoverProvenance(context.Background(), options.TrustedCore.Provenance()); err != nil {
			return nil, err
		}
		input, err := startupPolicyInput(context.Background(), options, subscriptions, settings)
		if err != nil {
			return nil, err
		}
		if _, err := options.TrustedCore.InstalledAvailable(context.Background()); err != nil {
			return nil, err
		}
		if err = options.TrustedCore.InitializeConfig(context.Background(), settings, input); err != nil {
			return nil, err
		}
		installer = options.TrustedCore.Installer()
	} else if err := core.EnsureRuntimeConfig(paths.RuntimeConfig, settings); err != nil {
		return nil, err
	}
	if err := probeManagedPorts(settings, nil); err != nil {
		return nil, err
	}

	store := state.NewStore(state.Snapshot{
		Version:   daemonVersion,
		StartedAt: time.Now().UTC(),
		Health:    "ok",
	})
	if info, err := os.Stat(paths.CoreBinary); err == nil && !info.IsDir() {
		if version, err := installer.DetectVersion(context.Background(), paths.CoreBinary); err == nil {
			snapshot := store.Load()
			snapshot.Core = state.CoreState{Status: "stopped", Version: version, Channel: settings.CoreChannel}
			store.Store(snapshot)
		}
	}
	coordinator := state.NewCoordinator(store)
	controller := mihomo.NewClient("http://"+settings.ControllerAddr, settings.ControllerSecret, nil)
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
	var onboardingWarning func(error)
	if options.OnBackgroundError != nil {
		onboardingWarning = func(err error) { options.OnBackgroundError("onboarding", err) }
	}
	onboardingService, err := onboarding.Open(onboarding.Options{
		StatePath:            paths.Onboarding,
		InitialSetupRequired: options.InitialSetupRequired,
		SaveState:            options.SaveOnboardingState,
		OnPersistenceWarning: onboardingWarning,
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
	starterStdout, starterStderr := stdout, stderr
	if options.MihomoStdout != nil {
		starterStdout = options.MihomoStdout
	}
	if options.MihomoStderr != nil {
		starterStderr = options.MihomoStderr
	}
	if starterStdout == nil {
		starterStdout = io.Discard
	}
	if starterStderr == nil {
		starterStderr = io.Discard
	}
	mihomoStarter := supervisor.CommandStarter{
		BinaryPath: paths.CoreBinary,
		DataDir:    paths.Root,
		ConfigPath: paths.RuntimeConfig,
		Stdout:     starterStdout,
		Stderr:     starterStderr,
	}
	if options.TrustedCore != nil {
		mihomoStarter.CommandFactory = options.TrustedCore.RunCommand
	}
	var manager *runtimeapi.Manager
	coreSupervisor := supervisor.New(supervisor.Options{
		Starter: mihomoStarter,
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
		Store:           store,
		Coordinator:     coordinator,
		Installer:       installer,
		TrustedCore:     options.TrustedCore,
		RootConfigInput: options.RootConfigInput,
		Resources:       concreteResourcePreparer(options.Resources),
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
		Onboarding:        onboardingService,
		Logging:           options.Logging,
		RefreshLogSecrets: options.RefreshLogSecrets,
		Panels:            panelService,
		WebGateway:        webGateway,
		WebOpenToken:      webCredential,
		Settings:          settings,
		SettingsPath:      settingsPath,
		ServiceStatus:     options.ServiceStatus,
		OnBackgroundError: options.OnBackgroundError,
		SysProxy:          sysproxy.Platform(),
		TunDetect:         tundetect.Platform(),
		RuntimeConfig:     paths.RuntimeConfig,
		StagingDir:        paths.SubscriptionStaging,
		ValidateConfig: func(ctx context.Context, candidatePath string) error {
			if options.TrustedCore != nil {
				return protocol.APIError{Code: protocol.CodeInvalidState, Message: "root config requires a generated capability"}
			}
			return core.ValidateConfig(ctx, core.OSCommandRunner{}, paths.CoreBinary, paths.Root, candidatePath)
		},
		RunScheduler: func(ctx context.Context) error {
			var schedulers sync.WaitGroup
			workers := 2
			if concreteResourcePreparer(options.Resources) != nil {
				workers++
			}
			schedulers.Add(workers)
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
			if concreteResourcePreparer(options.Resources) != nil {
				go func() {
					defer schedulers.Done()
					_ = manager.RunProviderScheduler(ctx)
				}()
			}
			<-ctx.Done()
			schedulers.Wait()
			return nil
		},
		BinaryExists: func() bool {
			if options.TrustedCore != nil {
				ready, err := options.TrustedCore.InstalledAvailable(context.Background())
				return err == nil && ready
			}
			info, err := os.Stat(paths.CoreBinary)
			return err == nil && !info.IsDir()
		},
	})
	webGateway.Mutator = webMutator{manager: manager}
	return &RuntimeAssembly{Manager: manager, Store: store, Web: webGateway, mihomoStarter: mihomoStarter}, nil
}

func concreteResourcePreparer(resources StartupResources) *subscription.ResourcePreparer {
	preparer, _ := resources.(*subscription.ResourcePreparer)
	return preparer
}

func startupPolicyInput(ctx context.Context, options RuntimeBuildOptions, subscriptions *subscription.Service, settings config.Settings) (subscription.PolicyInput, error) {
	catalog := subscriptions.Snapshot()
	if catalog.ActiveID == "" {
		input, err := options.RootConfigInput(ctx, nil, settings)
		if err != nil {
			return subscription.PolicyInput{}, err
		}
		input.YAML = []byte("proxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n")
		input.Settings = settings
		input.Resources = nil
		return input, nil
	}
	index := catalog.Index(catalog.ActiveID)
	if index < 0 || catalog.Profiles[index].Generation == 0 {
		return subscription.PolicyInput{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "active subscription cache is unavailable"}
	}
	profile := catalog.Profiles[index]
	if options.Resources == nil {
		return subscription.PolicyInput{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "managed active resources are unavailable"}
	}
	_, document, err := subscriptions.ReadCache(profile.ID)
	if err != nil {
		return subscription.PolicyInput{}, err
	}
	input, err := options.RootConfigInput(ctx, document, settings)
	if err != nil {
		return subscription.PolicyInput{}, err
	}
	input.YAML, err = yaml.Marshal(document)
	if err != nil {
		return subscription.PolicyInput{}, err
	}
	input.SubscriptionID = profile.ID
	input.Generation = profile.Generation
	input.Settings = settings
	input.Resources = nil
	graph, err := options.Resources.SnapshotResources(ctx, input)
	if err != nil {
		return subscription.PolicyInput{}, err
	}
	return options.Resources.OfflineInput(ctx, input, graph)
}

type webMutationRuntime interface {
	SelectProxy(context.Context, runtimeapi.Operation, string, string) error
	CloseConnection(context.Context, runtimeapi.Operation, string) error
	CloseAllConnections(context.Context, runtimeapi.Operation) error
	EnableTun(context.Context, runtimeapi.Operation, bool) (protocol.TunStatus, error)
	DisableTun(context.Context, runtimeapi.Operation) (protocol.TunStatus, error)
}

// webMutator routes browser mutations through the daemon coordinator.
type webMutator struct {
	manager webMutationRuntime
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
		_, err := m.manager.EnableTun(ctx, op, false)
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
