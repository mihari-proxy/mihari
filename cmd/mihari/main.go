package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/tui"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
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
	PrepareRuntime    func(context.Context, config.Settings) (app.RuntimeBuildOptions, error)
	MachineSnapshot   bool
	Paths             platform.Paths
	PrivateFS         *platform.PrivateFS
	Token             string
	Version           string
	Endpoint          string
	Ready             chan<- struct{}
	ServiceStatus     func() (string, error)
	LoadSettings      func(path, sidecar string) (config.Settings, bool, config.CommitResult, error)
	ValidationMode    bool
	ValidationReady   func(bool) error
	Listen            func(context.Context) (net.Listener, error)
	RuntimeOptions    app.RuntimeBuildOptions
	ActivationPhase   string
	ShareProcessGroup bool
	// DiagnosticStderr is the daemon-owned fallback for failures before its
	// structured logger is available. Nil deliberately suppresses diagnostics
	// so CLI rendering keeps ownership of its text and JSON output.
	DiagnosticStderr io.Writer
	// LoggingFailureStderr is the independent sink already owned by the logging
	// runtime for its own write and close failures. JSON invocations leave it nil.
	LoggingFailureStderr io.Writer
}

type daemonLoggingResources struct {
	StdoutCapture io.Closer
	StderrCapture io.Closer
	MihomoRuntime io.Closer
	DaemonRuntime io.Closer
	PrivateFS     io.Closer
}

type daemonLoggingRuntime struct {
	Closer  io.Closer
	Logger  *slog.Logger
	Runtime *logging.Runtime
}

// daemonServiceDiagnosticStderr selects the explicit service-owned diagnostic
// channel. Foreground and JSON CLI invocations retain renderer ownership.
func daemonServiceDiagnosticStderr(args []string, stderr io.Writer) io.Writer {
	if stderr == nil || daemonJSONOutput(args) {
		return nil
	}
	if enabled, _ := commandBooleanFlag(args, "system-service"); enabled {
		return stderr
	}
	return nil
}

func daemonLoggingFailureStderr(args []string, stderr io.Writer) io.Writer {
	if stderr == nil || daemonJSONOutput(args) || !daemonInvocation(args) {
		return nil
	}
	return stderr
}

func daemonJSONOutput(args []string) bool {
	enabled, _ := commandBooleanFlag(args, "json")
	return enabled
}

func daemonInvocation(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if _, recognized := commandBooleanFlagArgument(arg, "json"); recognized {
			continue
		}
		return arg == "daemon"
	}
	return false
}

func commandBooleanFlag(args []string, name string) (value bool, changed bool) {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		parsed, recognized := commandBooleanFlagArgument(arg, name)
		if recognized {
			value, changed = parsed, true
		}
	}
	return value, changed
}

func commandBooleanFlagArgument(arg, name string) (bool, bool) {
	flag := "--" + name
	if arg == flag {
		return true, true
	}
	prefix := flag + "="
	if !strings.HasPrefix(arg, prefix) {
		return false, false
	}
	value, err := strconv.ParseBool(strings.TrimPrefix(arg, prefix))
	return value, err == nil
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
		return daemonLoggingRuntime{Closer: runtime, Logger: runtime.Logger(), Runtime: runtime}, nil
	}
	newDaemonCapture   = logging.NewLineCaptureWriter
	buildDaemonRuntime = app.BuildRuntimeWithOptions
	runDaemon          = daemon.Run
)

func main() {
	os.Exit(runWithProcessContext(func(ctx context.Context) int {
		return executeProcess(ctx, os.Args[1:], os.Stdout, os.Stderr)
	}))
}

var exportLogsFn = logging.Export

func buildExportLogs(paths platform.Paths) func(tui.LoggingResources) ui.ExportLogsOptions {
	return func(resources tui.LoggingResources) ui.ExportLogsOptions {
		exists := func(dir, name string) (bool, error) {
			if resources.PrivateFS == nil || filepath.Clean(dir) != filepath.Clean(paths.LogExportDir) {
				return false, nil
			}
			publishDir, err := resources.PrivateFS.OpenPublishDir(paths.LogExportDir)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return false, nil
				}
				return false, err
			}
			exists, probeErr := publishDir.Exists(name)
			closeErr := publishDir.Close()
			return exists, errors.Join(probeErr, closeErr)
		}
		export := func(ctx context.Context, request logging.ExportRequest) (logging.ExportResult, error) {
			if resources.PrivateFS == nil {
				return logging.ExportResult{}, ui.ErrLocalLogStorageUnavailable
			}
			request.Paths = logging.ExportPaths{LogDir: paths.LogDir, ExportDir: paths.LogExportDir, DaemonLog: paths.DaemonLog, TUILog: paths.TUILog, MihomoLog: paths.MihomoLog}
			request.PrivateFS = resources.PrivateFS
			request.Redactor = resources.Redactor
			if resources.Runtime != nil {
				request.EnterRecordMutex = func(basePath string) func() {
					if filepath.Clean(basePath) == filepath.Clean(paths.TUILog) {
						return resources.Runtime.EnterRecordMutex()
					}
					return func() {}
				}
			}
			return exportLogsFn(ctx, request)
		}
		return ui.ExportLogsOptions{DefaultDir: paths.LogExportDir, Exists: exists, Export: export}
	}
}

type assembledExportOptions struct {
	Scope               string
	UserLogs            platform.Paths
	Resources           tui.LoggingResources
	OpenMachineSnapshot func(context.Context, logging.SnapshotWindow) (logging.SnapshotSet, error)
	LocalSource         logging.SnapshotSource
	AttachStream        func(io.Closer)
	Assemble            func(context.Context, logging.ExportRequest, string, []logging.NamedSource, func(context.Context) error) (logging.ExportResult, error)
}

type snapshotResponse struct {
	mu     sync.Mutex
	closer io.Closer
}

func (s *snapshotResponse) Set(c io.Closer) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closer = c
	s.mu.Unlock()
}

func (s *snapshotResponse) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	c := s.closer
	s.closer = nil
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Close()
}

func snapshotWindowFromRequest(request logging.ExportRequest) logging.SnapshotWindow {
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	switch request.Range.Kind {
	case logging.RangeAll:
		return logging.SnapshotWindow{To: now}
	case logging.RangeLast24Hours:
		from := now.Add(-24 * time.Hour)
		return logging.SnapshotWindow{From: &from, To: now}
	case logging.RangeLast60Minutes:
		from := now.Add(-60 * time.Minute)
		return logging.SnapshotWindow{From: &from, To: now}
	default:
		from := request.Range.From.UTC()
		to := request.Range.To.UTC()
		if to.IsZero() {
			to = now
		}
		return logging.SnapshotWindow{From: &from, To: to}
	}
}

func localTUINamedSource(options assembledExportOptions) logging.NamedSource {
	source := options.LocalSource
	if source == nil {
		source = logging.NewFileSnapshotSource(logging.TUISource, logging.MachineSnapshotOptions{
			PrivateFS: options.Resources.PrivateFS,
			Paths: logging.ExportPaths{
				LogDir:    options.UserLogs.LogDir,
				ExportDir: options.UserLogs.LogExportDir,
				TUILog:    options.UserLogs.TUILog,
			},
			Redactor: options.Resources.Redactor,
			EnterRecordMutex: func(ctx context.Context, path string) (func(), error) {
				if options.Resources.Runtime == nil || filepath.Clean(path) != filepath.Clean(options.UserLogs.TUILog) {
					return func() {}, nil
				}
				return options.Resources.Runtime.EnterRecordMutexContext(ctx)
			},
		})
	}
	return logging.NamedSource{ID: logging.TUISource, Source: source}
}

func exportAssembledLogs(ctx context.Context, request logging.ExportRequest, options assembledExportOptions) (logging.ExportResult, error) {
	if options.Resources.PrivateFS == nil {
		return logging.ExportResult{}, ui.ErrLocalLogStorageUnavailable
	}
	assemble := options.Assemble
	if assemble == nil {
		assemble = logging.Assemble
	}
	window := snapshotWindowFromRequest(request)
	request.Paths = logging.ExportPaths{
		LogDir:    options.UserLogs.LogDir,
		ExportDir: options.UserLogs.LogExportDir,
		TUILog:    options.UserLogs.TUILog,
	}
	request.PrivateFS = options.Resources.PrivateFS
	request.Redactor = options.Resources.Redactor
	tuiSource := localTUINamedSource(options)
	switch options.Scope {
	case logging.ExportScopeCurrentUserOnly:
		return assemble(ctx, request, logging.ExportScopeCurrentUserOnly, []logging.NamedSource{tuiSource}, nil)
	case logging.ExportScopeMachineAndCurrentUser, "":
		if options.OpenMachineSnapshot == nil {
			return logging.ExportResult{}, ui.ErrMachineLogsUnavailable
		}
		set, err := options.OpenMachineSnapshot(ctx, window)
		if err != nil {
			return logging.ExportResult{}, err
		}
		if options.AttachStream != nil {
			options.AttachStream(set)
		}
		defer func() { _ = set.Close() }()
		sources := append(logging.NamedSourcesFromSet(set), tuiSource)
		return assemble(ctx, request, logging.ExportScopeMachineAndCurrentUser, sources, set.Finish)
	default:
		return logging.ExportResult{}, logging.ErrInvalidExportRequest
	}
}

func buildSystemExportLogs(userLogs platform.Paths, openSnapshot func(context.Context, logging.SnapshotWindow) (logging.SnapshotSet, error)) func(tui.LoggingResources) ui.ExportLogsOptions {
	return func(resources tui.LoggingResources) ui.ExportLogsOptions {
		stream := &snapshotResponse{}
		exists := func(dir, name string) (bool, error) {
			if resources.PrivateFS == nil || filepath.Clean(dir) != filepath.Clean(userLogs.LogExportDir) {
				return false, nil
			}
			publishDir, err := resources.PrivateFS.OpenPublishDir(userLogs.LogExportDir)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return false, nil
				}
				return false, err
			}
			exists, probeErr := publishDir.Exists(name)
			closeErr := publishDir.Close()
			return exists, errors.Join(probeErr, closeErr)
		}
		return ui.ExportLogsOptions{
			DefaultDir:       userLogs.LogExportDir,
			Exists:           exists,
			SourcesPrompt:    true,
			MachineAvailable: func() bool { return openSnapshot != nil },
			CloseResponse:    func() { _ = stream.Close() },
			ExportScoped: func(ctx context.Context, request logging.ExportRequest, scope string) (logging.ExportResult, error) {
				return exportAssembledLogs(ctx, request, assembledExportOptions{
					Scope:               scope,
					UserLogs:            userLogs,
					Resources:           resources,
					OpenMachineSnapshot: openSnapshot,
					AttachStream:        stream.Set,
				})
			},
			Export: func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
				return logging.ExportResult{}, ui.ErrMachineLogsUnavailable
			},
		}
	}
}

func openTUILogging(ctx context.Context, paths platform.Paths, token string, fs *platform.PrivateFS, errorOutput io.Writer) (tui.LoggingResources, error) {
	redactor := logging.NewRedactor(token)
	if fs == nil {
		return tui.NewLoggingResources(nil, redactor, nil), errors.New("TUI logging private fs is unavailable")
	}
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		return tui.NewLoggingResources(nil, redactor, fs), err
	}
	runtime, err := logging.Open(ctx, logging.RuntimeOptions{
		BasePath:  paths.TUILog,
		Component: "tui",
		Config:    logging.BootstrapConfig(),
		PrivateFS: fs,
		Redactor:  redactor,
		Reporter:  logging.NewFailureReporter(errorOutput, redactor, nil),
	})
	if err != nil {
		return tui.NewLoggingResources(nil, redactor, fs), err
	}
	return tui.NewLoggingResources(runtime, redactor, fs), nil
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
	diagnosticStderr := deps.DiagnosticStderr
	if deps.PrivateFS == nil {
		reportDaemonStartupFailure(diagnosticStderr, "open data root", nil)
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "create mihari data directories"}
	}
	resources := newDaemonResources(deps.PrivateFS)
	redactor := logging.NewRedactor()
	reporter := logging.NewFailureReporter(deps.LoggingFailureStderr, redactor, nil)
	logSecretsReady := false
	var closeOnce sync.Once
	closeResources := func() error {
		var closeErr error
		closeOnce.Do(func() {
			closeErr = resources.Close()
			// Logging is closed. Keep its final failure on the independent outlet;
			// before secret registration only a fixed summary is safe.
			if closeErr != nil {
				failure := closeErr
				if !logSecretsReady {
					failure = errors.New("close logging resources failed")
				}
				reporter.Report(logging.FailureCleanup, failure)
			}
		})
		return closeErr
	}
	defer func() {
		resultErr = errors.Join(resultErr, closeResources())
	}()
	if !deps.ValidationMode && !daemon.BusinessMutationAllowed(deps.ActivationPhase, false) {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "install recovery required"}
	}

	if deps.ValidationMode && deps.ValidationReady == nil {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "authenticated validation lifecycle is required"}
	}
	if !deps.ValidationMode {
		if err := deps.Paths.EnsureDirs(); err != nil {
			reportDaemonStartupFailure(diagnosticStderr, "create data directories", err)
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "create mihari data directories"}
		}
	}

	sidecar := filepath.Join(deps.Paths.Bin, "core-channel")
	loadSettings := deps.LoadSettings
	if loadSettings == nil {
		loadSettings = config.LoadOrCreateWithSidecarOutcome
	}
	var (
		settings       config.Settings
		created        bool
		settingsCommit config.CommitResult
		err            error
	)
	if deps.ValidationMode {
		settings, err = config.Load(deps.Paths.Settings)
		if errors.Is(err, os.ErrNotExist) {
			settings = config.Defaults()
			err = nil
			created = true
		}
	} else {
		settings, created, settingsCommit, err = loadSettings(deps.Paths.Settings, sidecar)
	}
	if err != nil {
		failure := protocol.APIError{Code: protocol.CodeDataFailure, Message: "load settings"}
		reportDaemonStartupFailure(diagnosticStderr, "load settings", err)
		if deps.Listen == nil || errors.Is(err, os.ErrPermission) {
			return failure
		}
		return runDegradedDaemon(ctx, deps, failure, nil, nil)
	}
	if err := deps.PrivateFS.EnsureDir(deps.Paths.LogDir); err != nil {
		reportDaemonStartupFailure(diagnosticStderr, "create log directory", err)
		return err
	}

	baseSecrets := collectBaseLogSecretsMode(deps.Paths, deps.Token, settings, deps.ValidationMode)
	catalogURLs := collectCatalogLogSecretsMode(deps.Paths, deps.ValidationMode)
	redactor.ReplaceExact(append(append([]string{}, baseSecrets...), catalogURLs...))
	for _, secret := range baseSecrets {
		redactor.RetainCredential(secret)
	}
	logSecretsReady = true
	cfg, err := daemonLoggingConfig(settings)
	if err != nil {
		reportDaemonStartupFailure(diagnosticStderr, "configure logging", err)
		return err
	}

	daemonRT, err := openDaemonRuntime(ctx, logging.RuntimeOptions{
		BasePath:  deps.Paths.DaemonLog,
		Component: "daemon",
		Config:    cfg,
		PrivateFS: deps.PrivateFS,
		Redactor:  redactor,
		Reporter:  reporter,
	})
	if err != nil {
		reportDaemonStartupFailure(diagnosticStderr, "open daemon log", err)
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
		reportDaemonStartupFailure(diagnosticStderr, "open mihomo log", err)
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
	loggingGroup := logging.NewGroup(deps.Paths.LogDir, cfg, daemonRT.Runtime, mihomoRT.Runtime)
	diagnosticReporter := logging.NewDiagnosticReporter(daemonRT.Logger, redactor)
	reportBackground := func(component string, bgErr error) {
		if bgErr == nil {
			return
		}
		daemonRT.Logger.Error(bgErr.Error(), slog.String("component", component))
	}
	if settingsCommit.Committed && settingsCommit.Warning != nil {
		reportDaemonDiagnostic(ctx, diagnosticReporter, diagnosticStderr, diagnostics.Record{
			Component: "daemon.settings",
			Event:     "settings_commit_warning",
			Level:     slog.LevelWarn,
			Err:       settingsCommit.Warning,
		})
	}

	options := deps.RuntimeOptions
	if deps.PrepareRuntime != nil {
		options, err = deps.PrepareRuntime(ctx, settings)
		if err != nil {
			return err
		}
	}
	options.InitialSetupRequired = created
	options.SettingsPath = deps.Paths.Settings
	options.ServiceStatus = deps.ServiceStatus
	options.Logging = loggingGroup
	options.DiagnosticReporter = diagnosticReporter
	options.RefreshLogSecrets = func(catalogURLs []string) {
		redactor.ReplaceExact(append(append([]string{}, baseSecrets...), catalogURLs...))
	}
	options.MihomoStdout, options.MihomoStderr = stdoutCapture, stderrCapture
	options.OnBackgroundError = reportBackground
	options.ValidationMode, options.ActivationPhase = deps.ValidationMode, deps.ActivationPhase
	options.ShareProcessGroup = deps.ShareProcessGroup
	var snapshot logging.MachineSnapshotSource
	if deps.MachineSnapshot && daemonRT.Runtime != nil && mihomoRT.Runtime != nil {
		snapshot = logging.NewMachineSnapshotSource(logging.MachineSnapshotOptions{PrivateFS: deps.PrivateFS, Paths: logging.ExportPaths{LogDir: deps.Paths.LogDir, DaemonLog: deps.Paths.DaemonLog, MihomoLog: deps.Paths.MihomoLog}, Redactor: redactor, EnterRecordMutex: func(ctx context.Context, path string) (func(), error) {
			switch filepath.Clean(path) {
			case filepath.Clean(deps.Paths.DaemonLog):
				return daemonRT.Runtime.EnterRecordMutexContext(ctx)
			case filepath.Clean(deps.Paths.MihomoLog):
				return mihomoRT.Runtime.EnterRecordMutexContext(ctx)
			default:
				return nil, os.ErrInvalid
			}
		}})
	}
	var assembly *app.RuntimeAssembly
	if deps.ValidationMode {
		assembly, err = app.BuildValidationRuntime(ctx, deps.Paths, settings, deps.Version, options)
	} else {
		assembly, err = buildDaemonRuntime(deps.Paths, settings, deps.Version, io.Discard, io.Discard, options)
	}

	if err != nil {
		if deps.ValidationMode {
			return err
		}
		reportDaemonDiagnostic(ctx, diagnosticReporter, diagnosticStderr, diagnostics.Record{
			Component: "daemon.startup",
			Event:     "runtime_build_failed",
			Level:     slog.LevelError,
			Err:       err,
		})
		var conflict *app.ManagedPortConflict
		if errors.As(err, &conflict) {
			store := app.NewDegradedStore(deps.Version, err)
			recovery, recoveryErr := app.NewPortRecovery(deps.Paths, settings, store, err, diagnosticReporter)
			if recoveryErr == nil {
				return runDaemon(ctx, daemon.Options{Listen: deps.Listen, Endpoint: deps.Endpoint, Token: deps.Token, Version: deps.Version, Ready: deps.Ready, Store: store, Onboarding: recovery, SnapshotSource: snapshot, DiagnosticReporter: diagnosticReporter})
			}
			reportDaemonDiagnostic(ctx, diagnosticReporter, diagnosticStderr, diagnostics.Record{Component: "daemon.startup", Event: "port_recovery_failed", Level: slog.LevelError, Err: recoveryErr})
			return runDegradedDaemon(ctx, deps, recoveryErr, snapshot, diagnosticReporter)
		}
		return runDegradedDaemon(ctx, deps, err, snapshot, diagnosticReporter)
	}
	var onReady func() error
	if deps.ValidationReady != nil {
		onReady = func() error { return deps.ValidationReady(assembly.SetupRequired) }
	}
	return runDaemon(ctx, daemon.Options{
		Listen: deps.Listen, OnReady: onReady, SnapshotSource: snapshot,
		Endpoint:           deps.Endpoint,
		Token:              deps.Token,
		Version:            deps.Version,
		Ready:              deps.Ready,
		Store:              assembly.Store,
		Runtime:            assembly.Manager,
		DiagnosticReporter: diagnosticReporter,
		ValidationMode:     deps.ValidationMode,
	})
}

func reportDaemonDiagnostic(ctx context.Context, reporter diagnostics.Reporter, fallback io.Writer, record diagnostics.Record) {
	if reporter != nil {
		reporter(ctx, record)
		return
	}
	reportDaemonStartupFailure(fallback, record.Event, record.Err)
}

func reportDaemonStartupFailure(out io.Writer, operation string, err error) {
	if out == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "mihari daemon startup: %s: %s\n", operation, safeStartupFailureSummary(err))
}

func safeStartupFailureSummary(err error) string {
	if err == nil {
		return "startup failed"
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) && pathError != nil {
		return "file operation " + safeStartupOperation(pathError.Op) + ": " + safeStartupErrorClass(pathError.Err)
	}
	var syscallError *os.SyscallError
	if errors.As(err, &syscallError) && syscallError != nil {
		return "system call " + safeStartupOperation(syscallError.Syscall) + ": " + safeStartupErrorClass(syscallError.Err)
	}
	return "startup failed"
}

func safeStartupOperation(operation string) string {
	if operation == "" {
		return "failed"
	}
	for _, character := range operation {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return "failed"
		}
	}
	return operation
}

func safeStartupErrorClass(err error) string {
	switch {
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, os.ErrNotExist):
		return "not found"
	case errors.Is(err, os.ErrExist):
		return "already exists"
	default:
		return "failed"
	}
}

func runDegradedDaemon(ctx context.Context, deps daemonRunDeps, cause error, snapshot logging.MachineSnapshotSource, diagnosticReporter diagnostics.Reporter) error {
	if deps.ValidationMode {
		return cause
	}
	return runDaemon(ctx, daemon.Options{Listen: deps.Listen, Endpoint: deps.Endpoint, Token: deps.Token, Version: deps.Version, Ready: deps.Ready, Store: app.NewDegradedStore(deps.Version, cause), SnapshotSource: snapshot, DiagnosticReporter: diagnosticReporter})
}

func daemonLoggingConfig(settings config.Settings) (logging.Config, error) {
	effective := settings.EffectiveLogging()
	cfg, err := logging.ConfigFromFields(effective.Level, effective.MaxSizeMB, effective.MaxFiles)
	if err != nil {
		return logging.Config{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid logging configuration"}
	}
	return cfg, nil
}

func collectBaseLogSecrets(paths platform.Paths, token string, settings config.Settings) []string {
	return collectBaseLogSecretsMode(paths, token, settings, false)
}

func collectBaseLogSecretsMode(paths platform.Paths, token string, settings config.Settings, readOnly bool) []string {
	secrets := make([]string, 0, 8)
	if token != "" {
		secrets = append(secrets, token)
	}
	if settings.ControllerSecret != "" {
		secrets = append(secrets, settings.ControllerSecret)
	}
	if readOnly {
		if cred, err := panel.LoadCredential(paths.WebCredential); err == nil && cred != "" {
			secrets = append(secrets, cred)
		}
		return secrets
	}
	if cred, err := panel.LoadOrCreateCredential(paths.WebCredential); err == nil && cred != "" {
		secrets = append(secrets, cred)
	}
	return secrets
}

func collectCatalogLogSecrets(paths platform.Paths) []string {
	return collectCatalogLogSecretsMode(paths, false)
}

func collectCatalogLogSecretsMode(paths platform.Paths, readOnly bool) []string {
	secrets := make([]string, 0, 8)
	var (
		catalog subscription.Catalog
		err     error
	)
	if readOnly {
		catalog, err = subscription.Load(paths.SubscriptionCatalog)
		if err != nil {
			return secrets
		}
	} else {
		catalog, err = subscription.LoadOrCreate(paths.SubscriptionCatalog)
		if err != nil {
			return secrets
		}
	}
	for _, profile := range catalog.Profiles {
		if profile.URL != "" {
			secrets = append(secrets, profile.URL)
		}
	}
	return secrets
}
