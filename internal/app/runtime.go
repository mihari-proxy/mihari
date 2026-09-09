package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
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
)

type RuntimeAssembly struct {
	SetupRequired bool
	Manager       *runtimeapi.Manager
	Store         *state.Store
	Web           *web.Server
	mihomoStarter supervisor.CommandStarter
}

type RuntimeBuildOptions struct {
	TrustedCore *core.TrustedExecution
	Resources   StartupResources
	// PortProbeListen opens temporary TCP listeners only for the managed-port probe.
	// Nil uses net.Listen; it does not replace controller or gateway listeners.
	PortProbeListen func(network, address string) (net.Listener, error)

	InitialSetupRequired bool
	SettingsPath         string
	ServiceStatus        func() (string, error)
	InstallationInspect  func(context.Context) (InstallationStatus, error)
	Logging              runtimeapi.LoggingRuntime
	DiagnosticReporter   diagnostics.Reporter
	RefreshLogSecrets    func(catalogURLs []string)
	MihomoStdout         io.Writer
	MihomoStderr         io.Writer
	SaveOnboardingState  func(string, onboarding.State) (config.CommitResult, error)
	OnBackgroundError    func(component string, err error)
	ValidationMode       bool
	ValidationCore       core.ProvenanceStore
	ActivationPhase      string
	// ShareProcessGroup is enabled only by the installed launchd service assembly.
	ShareProcessGroup bool
}

// StartupResources settles historical resource journals before business stores load.
type StartupResources interface{ Recover(context.Context) error }

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
	if options.ValidationMode {
		return buildValidationRuntime(paths, settings, daemonVersion, options)
	}
	if options.ActivationPhase != "" && options.ActivationPhase != InstallPhaseActivationCommitted && options.ActivationPhase != InstallPhaseComplete {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "install activation is required"}
	}
	if options.TrustedCore != nil {
		if err := options.TrustedCore.CheckPaths(paths.Root, paths.CoreBinary, paths.RuntimeConfig); err != nil {
			return nil, err
		}
	}
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}
	// Recover before opening catalog/onboarding or constructing any settings
	// consumer. The caller may have loaded settings before this recovery.
	if options.Resources != nil {
		if recovery, ok := options.Resources.(interface {
			RecoverState(context.Context) (*config.Settings, error)
		}); ok {
			recovered, err := recovery.RecoverState(context.Background())
			if err != nil {
				return nil, err
			}
			if recovered != nil {
				settings = recovered.Clone()
			}
		} else if err := options.Resources.Recover(context.Background()); err != nil {
			return nil, err
		}
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

		if err := core.RecoverProvenance(context.Background(), options.TrustedCore.Provenance()); err != nil {
			return nil, err
		}
		content, err := startupConfig(subscriptions, settings)
		if err != nil {
			return nil, err
		}
		if _, err := options.TrustedCore.InstalledAvailable(context.Background()); err != nil {
			return nil, err
		}
		if err = options.TrustedCore.InitializeConfig(context.Background(), content); err != nil {
			return nil, err
		}
		installer = options.TrustedCore.Installer()
	} else if err := core.EnsureRuntimeConfig(paths.RuntimeConfig, settings); err != nil {
		return nil, err
	}
	if err := probeManagedPortsWithListener(settings, nil, options.PortProbeListen); err != nil {
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
		ShareProcessGroup: options.ShareProcessGroup,
		BinaryPath:        paths.CoreBinary,
		DataDir:           paths.Root,
		ConfigPath:        paths.RuntimeConfig,
		Stdout:            starterStdout,
		Stderr:            starterStderr,
	}
	if options.TrustedCore != nil {
		mihomoStarter.CommandFactory = options.TrustedCore.RunCommand
	}
	var manager *runtimeapi.Manager
	coreSupervisor := supervisor.New(supervisor.Options{
		Starter:            mihomoStarter,
		DiagnosticReporter: options.DiagnosticReporter,
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
		ActivationPhase: options.ActivationPhase,
		Store:           store,
		Coordinator:     coordinator,
		Installer:       installer,
		TrustedCore:     options.TrustedCore,
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
		Onboarding:         onboardingService,
		Logging:            options.Logging,
		DiagnosticReporter: options.DiagnosticReporter,
		RefreshLogSecrets:  options.RefreshLogSecrets,
		Panels:             panelService,
		WebGateway:         webGateway,
		WebOpenToken:       webCredential,
		Settings:           settings,
		SettingsPath:       settingsPath,
		ServiceStatus:      options.ServiceStatus,
		InstallationStatus: installationStatusReader(options.InstallationInspect),
		OnBackgroundError:  options.OnBackgroundError,
		SysProxy:           sysproxy.Platform(),
		TunDetect:          tundetect.Platform(),
		RuntimeConfig:      paths.RuntimeConfig,
		StagingDir:         paths.SubscriptionStaging,
		ValidateConfig: func(ctx context.Context, candidatePath string) error {
			if options.TrustedCore != nil {
				return protocol.APIError{Code: protocol.CodeInvalidState, Message: "root config requires a generated capability"}
			}
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

func buildValidationRuntime(paths platform.Paths, settings config.Settings, daemonVersion string, options RuntimeBuildOptions) (*RuntimeAssembly, error) {
	return BuildValidationRuntime(context.Background(), paths, settings, daemonVersion, options)
}

// BuildValidationRuntime reads existing business objects without initializing
// after historical recovery, propagating private child lifetime cancellation.
func BuildValidationRuntime(ctx context.Context, paths platform.Paths, settings config.Settings, daemonVersion string, options RuntimeBuildOptions) (*RuntimeAssembly, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Resources != nil {
		if recovery, ok := options.Resources.(interface {
			RecoverState(context.Context) (*config.Settings, error)
		}); ok {
			recovered, err := recovery.RecoverState(ctx)
			if err != nil {
				return nil, err
			}
			if recovered != nil {
				settings = recovered.Clone()
			}
		} else if err := options.Resources.Recover(ctx); err != nil {
			return nil, err
		}
	}
	setupRequired := options.InitialSetupRequired
	onboardingState, err := onboarding.Load(paths.Onboarding)
	if errors.Is(err, os.ErrNotExist) {
		setupRequired = true
	} else if err != nil {
		return nil, err
	} else {
		setupRequired = setupRequired || !onboardingState.Complete
	}
	info, err := os.Stat(paths.CoreBinary)
	if errors.Is(err, os.ErrNotExist) {
		setupRequired = true
	} else if err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid installed core"}
	}
	if options.ValidationCore != nil {
		binary, err := options.ValidationCore.Inspect(ctx, core.InstalledBinary, "")
		if err != nil {
			return nil, err
		}
		receipt, err := options.ValidationCore.Inspect(ctx, core.InstalledReceipt, "")
		if err != nil {
			return nil, err
		}
		if binary.Present != receipt.Present {
			return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "incomplete installed core provenance pair"}
		}
		if binary.Present {
			verified, err := core.OpenInstalledCore(ctx, options.ValidationCore)
			if err != nil {
				return nil, err
			}
			if err := verified.Close(); err != nil {
				return nil, err
			}
		} else {
			setupRequired = true
		}
	}
	catalog, err := subscription.Load(paths.SubscriptionCatalog)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if catalog.ActiveID != "" {
		index := catalog.Index(catalog.ActiveID)
		if index < 0 || catalog.Profiles[index].Generation == 0 {
			return nil, startupCacheError()
		}
	}
	for _, profile := range catalog.Profiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if profile.Generation == 0 {
			continue
		}
		file, err := os.Open(filepath.Join(paths.SubscriptionCache, profile.ID+".yaml"))
		if err != nil {
			return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "subscription cache is unavailable"}
		}
		raw, readErr := io.ReadAll(io.LimitReader(validationContextReader{ctx: ctx, reader: file}, (16<<20)+1))
		if err = errors.Join(readErr, file.Close()); err != nil {
			return nil, err
		}
		document, err := subscription.ParseDocument(raw)
		if err != nil {
			return nil, err
		}
		if len(raw) > 16<<20 {
			return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "subscription cache exceeds size limit"}
		}
		if profile.ID == catalog.ActiveID {
			if _, err := subscription.Generate(document, nil, settings); err != nil {
				return nil, err
			}
		}

	}
	store := state.NewStore(state.Snapshot{
		Version:   daemonVersion,
		StartedAt: time.Now().UTC(),
		Health:    "ok",
	})
	manager := runtimeapi.New(runtimeapi.Options{
		Store:              store,
		Settings:           settings,
		SettingsPath:       options.SettingsPath,
		Logging:            options.Logging,
		DiagnosticReporter: options.DiagnosticReporter,
		ServiceStatus:      options.ServiceStatus,
		InstallationStatus: installationStatusReader(options.InstallationInspect),
		OnBackgroundError:  options.OnBackgroundError,
		ValidationMode:     true,
		ActivationPhase:    options.ActivationPhase,
	})
	return &RuntimeAssembly{Manager: manager, Store: store, SetupRequired: setupRequired}, nil
}

// startupConfig derives runtime YAML only from persisted source authority.
func startupConfig(subscriptions *subscription.Service, settings config.Settings) ([]byte, error) {
	catalog := subscriptions.Snapshot()
	document := subscription.Document{"proxies": []any{}, "proxy-groups": []any{}, "rules": []any{"MATCH,DIRECT"}}
	if catalog.ActiveID != "" {
		index := catalog.Index(catalog.ActiveID)
		if index < 0 || catalog.Profiles[index].Generation == 0 {
			return nil, startupCacheError()
		}
		_, cached, err := subscriptions.ReadCache(catalog.ActiveID)
		if err != nil {
			return nil, startupCacheError()
		}
		document = cached
	}
	content, err := subscription.Generate(document, nil, settings)
	if err != nil {
		return nil, startupCacheError()
	}
	return content, nil
}
func startupCacheError() error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "active subscription cache is unavailable"}
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

type validationContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r validationContextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(b) > 32768 {
		b = b[:32768]
	}
	return r.reader.Read(b)
}
