package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/tui"
	"github.com/mihari-proxy/mihari/internal/update"
)

var (
	localRootOnce   sync.Once
	localRootCached processLocalRoot
	localRootErr    error

	defaultAbsolutePaths = func() (platform.Paths, error) {
		return platform.DefaultPaths().Absolute()
	}
	newPrivateFS = platform.NewPrivateFS
	absPath      = filepath.Abs

	afterDaemonLoggingOpen func(*slog.Logger)
)

type processLocalRoot struct {
	Paths platform.Paths
	FS    *platform.PrivateFS
	Token string
}

type daemonRunDeps struct {
	Paths         platform.Paths
	PrivateFS     *platform.PrivateFS
	Token         string
	Version       string
	Endpoint      string
	Ready         chan<- struct{}
	ServiceStatus func() (string, error)
}

type daemonLoggingResources struct {
	StdoutCapture io.Closer
	StderrCapture io.Closer
	MihomoRuntime io.Closer
	DaemonRuntime io.Closer
	PrivateFS     io.Closer
}

type daemonLoggingRuntime struct {
	Closer io.Closer
	Logger *slog.Logger
}

var (
	newDaemonResources = func(privateFS io.Closer) *daemonLoggingResources {
		return &daemonLoggingResources{PrivateFS: privateFS}
	}
	openDaemonRuntime = func(ctx context.Context, options logging.RuntimeOptions) (daemonLoggingRuntime, error) {
		runtime, err := logging.Open(ctx, options)
		if err != nil {
			return daemonLoggingRuntime{}, err
		}
		return daemonLoggingRuntime{Closer: runtime, Logger: runtime.Logger()}, nil
	}
	newDaemonCapture   = logging.NewLineCaptureWriter
	buildDaemonRuntime = app.BuildRuntimeWithOptions
	runDaemon          = daemon.Run
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	endpoint := transport.DefaultEndpoint()
	localClient := controlclient.New(endpoint, "")
	var serviceManager *service.Manager
	ready := make(chan struct{})
	runDaemonBody := func(ctx context.Context) error {
		root, err := prepareLocalRoot()
		if err != nil {
			return err
		}
		return runDaemonWith(ctx, daemonRunDeps{
			Paths:     root.Paths,
			PrivateFS: root.FS,
			Token:     root.Token,
			Version:   buildinfo.Version,
			Endpoint:  endpoint,
			Ready:     ready,
			ServiceStatus: func() (string, error) {
				if serviceManager == nil {
					return string(service.StatusUnknown), nil
				}
				kind, err := serviceManager.Status()
				return string(kind), err
			},
		})
	}
	// When SCM launches ImagePath `mihari.exe daemon`, the process is non-interactive.
	// Manager.Run registers with the service control manager; a plain daemon body never
	// calls StartServiceCtrlDispatcher and Windows fails the start with a 30s timeout.
	serviceManager = service.New(service.Options{Run: runDaemonBody, Ready: ready})
	runDaemon := func(ctx context.Context) error {
		if !service.IsInteractive() {
			return serviceManager.Run()
		}
		return runDaemonBody(ctx)
	}
	selfUpdateCompletion := app.NewSelfUpdateServiceCompletion(serviceManager, localClient)
	selfUpdater := update.SelfUpdater{AfterReplace: selfUpdateCompletion.AfterReplace}
	executable, executableError := os.Executable()
	dependencies := cli.Dependencies{
		StatusClient:       localClient,
		RuntimeClient:      localClient,
		SubscriptionClient: localClient,
		PanelClient:        localClient,
		SystemProxyClient:  localClient,
		TunClient:          localClient,
		ServiceController:  serviceManager,
		SelfUpdater:        selfUpdater,
		OpenBrowser:        platform.OpenBrowser,
		Interactive:        isInteractiveTerminal(os.Stdin, os.Stdout),
		PrepareLocalRoot:   prepareLocalRootForClient(localClient),
		RunTUI: func(ctx context.Context) error {
			if executableError != nil {
				return protocol.APIError{Code: protocol.CodeInternal, Message: "resolve Mihari executable path"}
			}
			root, err := prepareLocalRoot()
			if err != nil {
				return err
			}
			return tui.Run(ctx, tui.Options{
				Client:         localClient,
				Service:        serviceManager,
				SelfUpdater:    selfUpdater,
				CurrentVersion: buildinfo.Version,
				BinaryPath:     executable,
				Elevated:       elevate.IsElevated,
				Relaunch: func() error {
					return relaunchWithLocalRootCleanup(func() error {
						return platform.Relaunch(executable, tuiRelaunchArgs(executable), os.Environ())
					})
				},
				Input:       os.Stdin,
				Output:      os.Stdout,
				ErrorOutput: os.Stderr,
				OpenLogging: func(ctx context.Context) (tui.LoggingResources, error) {
					return openTUILogging(ctx, root.Paths, root.Token, root.FS)
				},
			})
		},
		RunDaemon: runDaemon,
	}
	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, dependencies)
	_ = closeCachedLocalRoot()
	os.Exit(code)
}

func openTUILogging(ctx context.Context, paths platform.Paths, token string, fs *platform.PrivateFS) (tui.LoggingResources, error) {
	resources := tui.LoggingResources{PrivateFS: fs}
	resources.Health = &resources
	if fs == nil {
		resources.Redactor = logging.NewRedactor(token)
		return resources, errors.New("TUI logging private fs is unavailable")
	}
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		resources.Redactor = logging.NewRedactor(token)
		return resources, err
	}
	resources.Redactor = logging.NewRedactor(token)
	runtime, err := logging.Open(ctx, logging.RuntimeOptions{
		BasePath:  paths.TUILog,
		Component: "tui",
		Config:    logging.BootstrapConfig(),
		PrivateFS: fs,
		Redactor:  resources.Redactor,
	})
	if err != nil {
		return resources, err
	}
	resources.Runtime = runtime
	return resources, nil
}

func tuiRelaunchArgs(binary string) []string {
	return []string{binary}
}

func prepareLocalRootForClient(localClient *controlclient.Client) func() error {
	return func() error {
		root, err := prepareLocalRoot()
		if err != nil {
			return err
		}
		localClient.SetToken(root.Token)
		return nil
	}
}

func relaunchWithLocalRootCleanup(relaunch func() error) error {
	if err := closeCachedLocalRoot(); err != nil {
		return fmt.Errorf("close Mihari data root before relaunch: %w", err)
	}
	return relaunch()
}

func isInteractiveTerminal(input, output *os.File) bool {
	inputInfo, err := input.Stat()
	if err != nil || inputInfo.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	outputInfo, err := output.Stat()
	return err == nil && outputInfo.Mode()&os.ModeCharDevice != 0
}

func prepareLocalRoot() (processLocalRoot, error) {
	localRootOnce.Do(func() {
		localRootCached, localRootErr = doPrepareLocalRoot()
	})
	return localRootCached, localRootErr
}

func doPrepareLocalRoot() (processLocalRoot, error) {
	absolutePaths, err := defaultAbsolutePaths()
	if err != nil {
		return processLocalRoot{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "resolve Mihari data root"}
	}
	processFS, fsErr := newPrivateFS(absolutePaths.Root)
	credPath, err := resolveCredentialPath(absolutePaths)
	if err != nil {
		return processLocalRoot{Paths: absolutePaths, FS: processFS}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "resolve Mihari control credential"}
	}
	token, err := loadProcessToken(credPath, absolutePaths.Root, fsErr == nil)
	if err != nil {
		return processLocalRoot{Paths: absolutePaths, FS: processFS}, err
	}
	return processLocalRoot{Paths: absolutePaths, FS: processFS, Token: token}, nil
}

func resolveCredentialPath(absolutePaths platform.Paths) (string, error) {
	value := os.Getenv("MIHARI_CONTROL_CREDENTIAL")
	if value == "" {
		return absolutePaths.ControlToken, nil
	}
	abs, err := absPath(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func loadProcessToken(credPath, root string, fsOK bool) (string, error) {
	if fsOK || !isInsideRoot(credPath, root) {
		token, err := credential.LoadOrCreate(credPath)
		if err == nil {
			return token, nil
		}
		var apiError protocol.APIError
		if errors.As(err, &apiError) {
			if apiError.Code == "" {
				apiError.Code = protocol.CodeDataFailure
			}
			return "", apiError
		}
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "local control setup failed"}
	}
	token, err := credential.Load(credPath)
	if err != nil {
		return "", nil
	}
	return token, nil
}

func isInsideRoot(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func closeCachedLocalRoot() error {
	if localRootCached.FS == nil {
		return nil
	}
	return localRootCached.FS.Close()
}

func resetProcessLocalRoot() {
	if localRootCached.FS != nil {
		_ = localRootCached.FS.Close()
	}
	localRootOnce = sync.Once{}
	localRootCached = processLocalRoot{}
	localRootErr = nil
	defaultAbsolutePaths = func() (platform.Paths, error) {
		return platform.DefaultPaths().Absolute()
	}
	newPrivateFS = platform.NewPrivateFS
	absPath = filepath.Abs
	afterDaemonLoggingOpen = nil
}

func (r *daemonLoggingResources) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	for _, closer := range []io.Closer{r.StdoutCapture, r.StderrCapture, r.MihomoRuntime, r.DaemonRuntime, r.PrivateFS} {
		if closer == nil {
			continue
		}
		errs = append(errs, closer.Close())
	}
	return errors.Join(errs...)
}

func runDaemonWith(ctx context.Context, deps daemonRunDeps) (resultErr error) {
	if deps.PrivateFS == nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "create mihari data directories"}
	}
	resources := newDaemonResources(deps.PrivateFS)
	var closeOnce sync.Once
	closeResources := func() error {
		var closeErr error
		closeOnce.Do(func() {
			closeErr = resources.Close()
		})
		return closeErr
	}
	defer func() {
		resultErr = errors.Join(resultErr, closeResources())
	}()

	if err := deps.Paths.EnsureDirs(); err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "create mihari data directories"}
	}
	sidecar := filepath.Join(deps.Paths.Bin, "core-channel")
	settings, created, err := config.LoadOrCreateWithSidecar(deps.Paths.Settings, sidecar)
	if err != nil {
		return err
	}
	if err := deps.PrivateFS.EnsureDir(deps.Paths.LogDir); err != nil {
		return err
	}

	redactor := logging.NewRedactor()
	redactor.ReplaceExact(collectLogSecrets(deps.Paths, deps.Token, settings))
	reporter := logging.NewFailureReporter(os.Stderr, redactor, nil)
	cfg := logging.DefaultConfig()

	daemonRT, err := openDaemonRuntime(ctx, logging.RuntimeOptions{
		BasePath:  deps.Paths.DaemonLog,
		Component: "daemon",
		Config:    cfg,
		PrivateFS: deps.PrivateFS,
		Redactor:  redactor,
		Reporter:  reporter,
	})
	if err != nil {
		return err
	}
	resources.DaemonRuntime = daemonRT.Closer

	mihomoRT, err := openDaemonRuntime(ctx, logging.RuntimeOptions{
		BasePath:  deps.Paths.MihomoLog,
		Component: "mihomo",
		Config:    cfg,
		PrivateFS: deps.PrivateFS,
		Redactor:  redactor,
		Reporter:  reporter,
	})
	if err != nil {
		return err
	}
	resources.MihomoRuntime = mihomoRT.Closer

	stdoutCapture := newDaemonCapture(mihomoRT.Logger, slog.LevelInfo, "stdout")
	stderrCapture := newDaemonCapture(mihomoRT.Logger, slog.LevelWarn, "stderr")
	resources.StdoutCapture = stdoutCapture
	resources.StderrCapture = stderrCapture
	if afterDaemonLoggingOpen != nil {
		afterDaemonLoggingOpen(daemonRT.Logger)
	}

	assembly, err := buildDaemonRuntime(deps.Paths, settings, deps.Version, io.Discard, io.Discard, app.RuntimeBuildOptions{
		InitialSetupRequired: created,
		SettingsPath:         deps.Paths.Settings,
		ServiceStatus:        deps.ServiceStatus,
		MihomoStdout:         stdoutCapture,
		MihomoStderr:         stderrCapture,
		OnBackgroundError: func(component string, bgErr error) {
			if bgErr == nil {
				return
			}
			daemonRT.Logger.Error(bgErr.Error(), slog.String("component", component))
		},
	})
	if err != nil {
		return runDaemon(ctx, daemon.Options{
			Endpoint: deps.Endpoint,
			Token:    deps.Token,
			Version:  deps.Version,
			Ready:    deps.Ready,
			Store:    app.NewDegradedStore(deps.Version, err),
		})
	}
	return runDaemon(ctx, daemon.Options{
		Endpoint: deps.Endpoint,
		Token:    deps.Token,
		Version:  deps.Version,
		Ready:    deps.Ready,
		Store:    assembly.Store,
		Runtime:  assembly.Manager,
	})
}

func collectLogSecrets(paths platform.Paths, token string, settings config.Settings) []string {
	secrets := make([]string, 0, 8)
	if token != "" {
		secrets = append(secrets, token)
	}
	if settings.ControllerSecret != "" {
		secrets = append(secrets, settings.ControllerSecret)
	}
	if cred, err := panel.LoadOrCreateCredential(paths.WebCredential); err == nil && cred != "" {
		secrets = append(secrets, cred)
	}
	catalog, err := subscription.LoadOrCreate(paths.SubscriptionCatalog)
	if err != nil {
		return secrets
	}
	for _, profile := range catalog.Profiles {
		if profile.URL != "" {
			secrets = append(secrets, profile.URL)
		}
	}
	return secrets
}
