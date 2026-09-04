package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui"
)

func TestTUIRelaunchArgsStartsDefaultTUI(t *testing.T) {
	binary := `C:\Program Files\Mihari\mihari.exe`
	if got, want := tuiRelaunchArgs(binary), []string{binary}; !slices.Equal(got, want) {
		t.Fatalf("args=%q want=%q", got, want)
	}
}

func TestOpenTUILogging_UsesInjectedPaths(t *testing.T) {
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := openTUILogging(context.Background(), paths, "tui-control-token", fs, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if resources.PrivateFS != fs || resources.Redactor == nil || resources.Runtime == nil {
		t.Fatalf("resources=%+v", resources)
	}
	if resources.Health == nil || !resources.Health.Available() {
		t.Fatal("healthy TUI logger did not report available")
	}
	if _, isResourcePointer := resources.Health.(*tui.LoggingResources); isResourcePointer {
		t.Fatal("healthy TUI logger health points at a returned-value copy")
	}
	if got := resources.Runtime.Config(); got != logging.BootstrapConfig() {
		t.Fatalf("bootstrap config=%+v want=%+v", got, logging.BootstrapConfig())
	}
	resources.Runtime.Logger().Debug("TUI startup token=tui-control-token")
	copyOfResources := resources
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	if err := copyOfResources.Close(); err != nil {
		t.Fatal(err)
	}
	logged := readFileString(t, paths.TUILog)
	if strings.Contains(logged, "tui-control-token") || !strings.Contains(logged, "***") {
		t.Fatalf("TUI log was not redacted: %s", logged)
	}
}

func TestOpenTUILogging_RuntimeFailuresAreReported(t *testing.T) {
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	var errorOutput bytes.Buffer
	resources, err := openTUILogging(context.Background(), paths, "tui-control-token", fs, &errorOutput)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resources.Close() })
	held, err := platform.OpenAdvisoryLock(fs, paths.TUILog+".lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if err := held.Lock(context.Background(), platform.LockExclusive); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		resources.Runtime.Logger().Info("runtime write failed",
			slog.String("token", "tui-control-token"),
			slog.String("path", paths.TUILog),
		)
	}
	if err := held.Unlock(); err != nil {
		t.Fatal(err)
	}
	warnings := errorOutput.String()
	if got, want := warnings, "logging: dropped: lock wait exceeded\n"; got != want {
		t.Fatalf("runtime warnings=%q want=%q", got, want)
	}
	if strings.Contains(warnings, "tui-control-token") || strings.Contains(warnings, paths.Root) || strings.Contains(warnings, paths.TUILog) {
		t.Fatalf("runtime warning leaked secret or path: %q", warnings)
	}
}

func TestOpenTUILogging_NilPrivateFSDoesNotCreateRoot(t *testing.T) {
	paths := absoluteTempPaths(t)
	resources, err := openTUILogging(context.Background(), paths, "tui-control-token", nil, io.Discard)
	if err == nil {
		t.Fatal("nil PrivateFS did not report bootstrap failure")
	}
	if resources.PrivateFS != nil || resources.Redactor == nil {
		t.Fatalf("resources=%+v", resources)
	}
	if resources.Health == nil || resources.Health.Available() {
		t.Fatal("unavailable logger health was not retained")
	}
	if _, statErr := os.Stat(paths.Root); !os.IsNotExist(statErr) {
		t.Fatalf("nil PrivateFS created data root: %v", statErr)
	}
}

func TestOpenTUILogging_CanceledContextRetainsPartialResources(t *testing.T) {
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resources, err := openTUILogging(ctx, paths, "tui-control-token", fs, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context cancellation", err)
	}
	if resources.PrivateFS != fs || resources.Redactor == nil || resources.Runtime != nil {
		t.Fatalf("resources=%+v", resources)
	}
	if closeErr := resources.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestOpenTUILogging_EnsureDirFailureRetainsPartialResources(t *testing.T) {
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	resources, err := openTUILogging(context.Background(), paths, "tui-control-token", fs, io.Discard)
	if err == nil {
		t.Fatal("closed PrivateFS did not report EnsureDir failure")
	}
	if resources.PrivateFS != fs || resources.Redactor == nil || resources.Runtime != nil {
		t.Fatalf("resources=%+v", resources)
	}
	if resources.Health == nil || resources.Health.Available() {
		t.Fatal("EnsureDir failure health was not unavailable")
	}
	if closeErr := resources.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestInteractiveTerminal_RejectsRedirectedStreams(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	if isInteractiveTerminal(input, output) {
		t.Fatal("regular files must not be treated as an interactive terminal")
	}
}

func TestRelaunchWithLocalRootCleanup_ClosesPrivateFSBeforeRelaunch(t *testing.T) {
	resetProcessLocalRootForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	localRootCached = processLocalRoot{Paths: paths, FS: fs}

	called := false
	err = relaunchWithLocalRootCleanup(func() error {
		called = true
		if err := fs.EnsureDir(paths.LogDir); err == nil {
			t.Fatal("PrivateFS remained open when relaunch began")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("relaunch callback was not called")
	}
}

func TestDaemonLoggingResources_CloseOrderJoinsErrors(t *testing.T) {
	var order []string
	resources := &daemonLoggingResources{
		StdoutCapture: orderCloser{name: "stdout", order: &order, err: errors.New("stdout close")},
		StderrCapture: orderCloser{name: "stderr", order: &order, err: errors.New("stderr close")},
		MihomoRuntime: orderCloser{name: "mihomo", order: &order, err: errors.New("mihomo close")},
		DaemonRuntime: orderCloser{name: "daemon", order: &order, err: errors.New("daemon close")},
		PrivateFS:     orderCloser{name: "fs", order: &order, err: errors.New("fs close")},
	}
	err := resources.Close()
	wantOrder := []string{"stdout", "stderr", "mihomo", "daemon", "fs"}
	if !slices.Equal(order, wantOrder) {
		t.Fatalf("order=%q want=%q", order, wantOrder)
	}
	if err == nil {
		t.Fatal("expected joined close errors")
	}
	for _, part := range []string{"stdout close", "stderr close", "mihomo close", "daemon close", "fs close"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("joined error %q missing %q", err, part)
		}
	}
}

func TestRunDaemonWith_ClosesResourcesOnceForEveryExit(t *testing.T) {
	for _, test := range []struct {
		name      string
		openError string
		buildErr  error
		runErr    error
		wantOrder []string
	}{
		{name: "normal", wantOrder: []string{"run", "stdout", "stderr", "mihomo", "daemon", "fs"}},
		{name: "degraded", buildErr: errors.New("build failed"), wantOrder: []string{"run", "stdout", "stderr", "mihomo", "daemon", "fs"}},
		{name: "build runtime failure", buildErr: errors.New("build failed"), runErr: errors.New("daemon failed"), wantOrder: []string{"run", "stdout", "stderr", "mihomo", "daemon", "fs"}},
		{name: "daemon logging open failure", openError: "daemon", wantOrder: []string{"fs"}},
		{name: "mihomo logging open failure", openError: "mihomo", wantOrder: []string{"daemon", "fs"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetDaemonRunSeamsForTest(t)
			paths := absoluteTempPaths(t)
			fs, err := platform.NewPrivateFS(paths.Root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = fs.Close() })
			var order []string
			closers := map[string]*countingCloser{
				"stdout": {name: "stdout", order: &order},
				"stderr": {name: "stderr", order: &order},
				"mihomo": {name: "mihomo", order: &order},
				"daemon": {name: "daemon", order: &order},
				"fs":     {name: "fs", order: &order},
			}
			newDaemonResources = func(io.Closer) *daemonLoggingResources {
				return &daemonLoggingResources{PrivateFS: closers["fs"]}
			}
			openDaemonRuntime = func(_ context.Context, options logging.RuntimeOptions) (daemonLoggingRuntime, error) {
				if options.Component == test.openError {
					return daemonLoggingRuntime{}, errors.New("open " + options.Component)
				}
				return daemonLoggingRuntime{
					Closer: closers[options.Component],
					Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				}, nil
			}
			newDaemonCapture = func(_ *slog.Logger, _ slog.Level, stream string) logging.LineCaptureWriter {
				return &countingCapture{countingCloser: closers[stream]}
			}
			buildDaemonRuntime = func(platform.Paths, config.Settings, string, io.Writer, io.Writer, app.RuntimeBuildOptions) (*app.RuntimeAssembly, error) {
				if test.buildErr != nil {
					return nil, test.buildErr
				}
				return &app.RuntimeAssembly{}, nil
			}
			runDaemon = func(context.Context, daemon.Options) error {
				order = append(order, "run")
				return test.runErr
			}

			err = runDaemonWith(context.Background(), daemonRunDeps{Paths: paths, PrivateFS: fs, Token: "token", Version: "test"})
			if test.openError != "" {
				if err == nil || !strings.Contains(err.Error(), "open "+test.openError) {
					t.Fatalf("err=%v want open %s failure", err, test.openError)
				}
			} else if !errors.Is(err, test.runErr) {
				t.Fatalf("err=%v want=%v", err, test.runErr)
			}
			if !slices.Equal(order, test.wantOrder) {
				t.Fatalf("close order=%q want=%q", order, test.wantOrder)
			}
			for name, closer := range closers {
				wantCalls := 0
				if slices.Contains(test.wantOrder, name) {
					wantCalls = 1
				}
				if closer.calls != wantCalls {
					t.Fatalf("%s close calls=%d want=%d", name, closer.calls, wantCalls)
				}
			}
		})
	}
}

func TestRunDaemon_LoggingOpensBeforeBuildRuntime(t *testing.T) {
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	token := "exact-control-token-value"
	secret := strings.Repeat("c", 64)
	catalogURL := "https://provider.example/sub?token=catalog-secret-value"
	settings, holder := occupiedControllerSettings(t, secret)
	t.Cleanup(func() { _ = holder.Close() })
	writeSettings(t, paths, settings)
	writeCatalog(t, paths, catalogURL)

	afterDaemonLoggingOpen = func(logger *slog.Logger) {
		logger.Info("probe " + token + " " + secret + " " + catalogURL)
	}
	t.Cleanup(func() { afterDaemonLoggingOpen = nil })

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	endpoint := transporttest.Endpoint(t)
	done := make(chan error, 1)
	go func() {
		done <- runDaemonWith(ctx, daemonRunDeps{
			Paths:     paths,
			PrivateFS: fs,
			Token:     token,
			Version:   "test",
			Endpoint:  endpoint,
			Ready:     ready,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runDaemonWith did not stop")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("degraded daemon did not become ready")
	}
	daemonLog := readFileString(t, paths.DaemonLog)
	if strings.TrimSpace(daemonLog) == "" {
		t.Fatal("daemon JSONL missing after Open")
	}
	if _, err := os.Stat(paths.MihomoLog); err != nil {
		t.Fatalf("mihomo log missing: %v", err)
	}
	for _, secretValue := range []string{token, secret, catalogURL} {
		if strings.Contains(daemonLog, secretValue) {
			t.Fatalf("secret %q leaked: %s", secretValue, daemonLog)
		}
	}
}

func TestRunDaemon_CatalogLoadFailureStillOpensLogger(t *testing.T) {
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	settings, holder := occupiedControllerSettings(t, strings.Repeat("d", 64))
	t.Cleanup(func() { _ = holder.Close() })
	writeSettings(t, paths, settings)
	if err := os.MkdirAll(filepath.Dir(paths.SubscriptionCatalog), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SubscriptionCatalog, []byte("not: [valid: catalog"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	endpoint := transporttest.Endpoint(t)
	done := make(chan error, 1)
	go func() {
		done <- runDaemonWith(ctx, daemonRunDeps{
			Paths:     paths,
			PrivateFS: fs,
			Token:     "catalog-fail-token",
			Version:   "test",
			Endpoint:  endpoint,
			Ready:     ready,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runDaemonWith did not stop")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("degraded daemon did not become ready after catalog failure")
	}
	if _, err := os.Stat(paths.DaemonLog); err != nil {
		t.Fatalf("catalog failure returned before Open: %v", err)
	}
	if _, err := os.Stat(paths.MihomoLog); err != nil {
		t.Fatalf("mihomo log missing: %v", err)
	}
}

func TestCollectLogSecretsReadsExistingBusinessSecrets(t *testing.T) {
	paths := absoluteTempPaths(t)
	settings := config.Defaults()
	settings.ControllerSecret = "controller-secret"
	webCredential := strings.Repeat("f", 64)
	catalogURL := "https://provider.example/sub?token=catalog-secret"
	if err := os.MkdirAll(filepath.Dir(paths.WebCredential), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.WebCredential, []byte(webCredential+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeCatalog(t, paths, catalogURL)

	got := collectLogSecrets(paths, "control-token", settings)
	want := []string{"control-token", "controller-secret", webCredential, catalogURL}
	if !slices.Equal(got, want) {
		t.Fatalf("secrets=%q want=%q", got, want)
	}
}

func TestRunDaemon_NilPrivateFSReturnsDataFailure(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	err := runDaemonWith(context.Background(), daemonRunDeps{
		Paths:    paths,
		Token:    "token-value",
		Version:  "test",
		Endpoint: transporttest.Endpoint(t),
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(paths.Settings); !os.IsNotExist(statErr) {
		t.Fatalf("nil fs performed settings IO: %v", statErr)
	}
	if _, statErr := os.Stat(paths.Root); !os.IsNotExist(statErr) {
		t.Fatalf("nil fs performed directory IO: %v", statErr)
	}
}

func TestResolveCredentialPath_RelativeAndAbsolute(t *testing.T) {
	resetProcessLocalRootForTest(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	paths, err := platform.NewPaths(filepath.Join(cwd, "data")).Absolute()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("MIHARI_CONTROL_CREDENTIAL", "")
	got, err := resolveCredentialPath(paths)
	if err != nil || got != paths.ControlToken {
		t.Fatalf("default path=%q err=%v want=%q", got, err, paths.ControlToken)
	}

	t.Setenv("MIHARI_CONTROL_CREDENTIAL", "relative.token")
	got, err = resolveCredentialPath(paths)
	wantRelative, absErr := filepath.Abs("relative.token")
	if absErr != nil {
		t.Fatal(absErr)
	}
	wantRelative = filepath.Clean(wantRelative)
	if err != nil || got != wantRelative {
		t.Fatalf("relative path=%q err=%v want=%q", got, err, wantRelative)
	}

	outside := filepath.Join(t.TempDir(), "outside.token")
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", outside)
	got, err = resolveCredentialPath(paths)
	if err != nil || got != filepath.Clean(outside) {
		t.Fatalf("absolute path=%q err=%v want=%q", got, err, outside)
	}
}

func TestPrepareLocalRoot_RelativeDataRoot(t *testing.T) {
	resetProcessLocalRootForTest(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("MIHARI_DATA", "relative-data")
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", "")
	root, err := prepareLocalRoot()
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := filepath.Abs("relative-data")
	if err != nil {
		t.Fatal(err)
	}
	wantRoot = filepath.Clean(wantRoot)
	if root.Paths.Root != wantRoot {
		t.Fatalf("root=%q want=%q", root.Paths.Root, wantRoot)
	}
	if root.FS == nil || root.Token == "" {
		t.Fatalf("fs=%v token empty=%v", root.FS, root.Token == "")
	}
	if root.Paths.ControlToken != filepath.Join(wantRoot, "control.token") {
		t.Fatalf("token path=%q", root.Paths.ControlToken)
	}
	if root.Paths.Settings != filepath.Join(wantRoot, "mihari.yaml") {
		t.Fatalf("settings=%q", root.Paths.Settings)
	}
	if root.Paths.DaemonLog != filepath.Join(wantRoot, "logs", "mihari-daemon.log") {
		t.Fatalf("daemon log=%q", root.Paths.DaemonLog)
	}
	resources, err := openTUILogging(context.Background(), root.Paths, root.Token, root.FS, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if resources.PrivateFS != root.FS || resources.Runtime == nil {
		t.Fatalf("resources=%+v", resources)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareLocalRoot_UnsetDataUsesHome(t *testing.T) {
	resetProcessLocalRootForTest(t)
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("MIHARI_DATA", "")
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", "")
	root, err := prepareLocalRoot()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".mihari")
	if root.Paths.Root != want {
		t.Fatalf("root=%q want=%q", root.Paths.Root, want)
	}
}

func TestPrepareLocalRoot_AbsFailureDoesNoIO(t *testing.T) {
	resetProcessLocalRootForTest(t)
	rootDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("MIHARI_DATA", rootDir)
	defaultAbsolutePaths = func() (platform.Paths, error) {
		return platform.Paths{}, errors.New("abs failed")
	}
	_, err := prepareLocalRoot()
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "resolve Mihari data root" {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(rootDir); !os.IsNotExist(statErr) {
		t.Fatalf("data root IO after abs failure: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(rootDir, "control.token")); !os.IsNotExist(statErr) {
		t.Fatalf("token IO after abs failure: %v", statErr)
	}
}

func TestPrepareLocalRoot_NewPrivateFSFailureSkipsInRootCreate(t *testing.T) {
	resetProcessLocalRootForTest(t)
	rootDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("MIHARI_DATA", rootDir)
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", "")
	newPrivateFS = func(string) (*platform.PrivateFS, error) {
		return nil, errors.New("injected fs failure")
	}
	root, err := prepareLocalRoot()
	if err != nil {
		t.Fatalf("SetupError=%v", err)
	}
	if root.FS != nil {
		t.Fatal("expected nil PrivateFS")
	}
	if root.Token != "" {
		t.Fatalf("token=%q", root.Token)
	}
	if _, statErr := os.Stat(rootDir); !os.IsNotExist(statErr) {
		t.Fatalf("MkdirAll/EnsureDirs created root: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(rootDir, "control.token")); !os.IsNotExist(statErr) {
		t.Fatalf("LoadOrCreate created token: %v", statErr)
	}
}

func TestPrepareLocalRoot_ExplicitInRootCredentialSkipsCreateAfterFSFailure(t *testing.T) {
	resetProcessLocalRootForTest(t)
	rootDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("MIHARI_DATA", rootDir)
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", filepath.Join(rootDir, "custom.token"))
	newPrivateFS = func(string) (*platform.PrivateFS, error) {
		return nil, errors.New("injected fs failure")
	}
	root, err := prepareLocalRoot()
	if err != nil {
		t.Fatalf("SetupError=%v", err)
	}
	if root.FS != nil || root.Token != "" {
		t.Fatalf("fs=%v token=%q", root.FS, root.Token)
	}
	if _, statErr := os.Stat(filepath.Join(rootDir, "custom.token")); !os.IsNotExist(statErr) {
		t.Fatalf("in-root credential was created: %v", statErr)
	}
}

func TestPrepareLocalRoot_ExplicitOutOfRootCredentialCreatesAfterFSFailure(t *testing.T) {
	resetProcessLocalRootForTest(t)
	rootDir := filepath.Join(t.TempDir(), "data")
	outside := filepath.Join(t.TempDir(), "outside.token")
	t.Setenv("MIHARI_DATA", rootDir)
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", outside)
	newPrivateFS = func(string) (*platform.PrivateFS, error) {
		return nil, errors.New("injected fs failure")
	}
	root, err := prepareLocalRoot()
	if err != nil {
		t.Fatal(err)
	}
	if root.FS != nil {
		t.Fatal("expected nil PrivateFS")
	}
	if root.Token == "" {
		t.Fatal("expected out-of-root token")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("expected LoadOrCreate of outside credential: %v", err)
	}
	if _, statErr := os.Stat(rootDir); !os.IsNotExist(statErr) {
		t.Fatalf("data root created: %v", statErr)
	}
}

func TestPrepareLocalRoot_ExplicitOutOfRootCredentialCreatesTUILog(t *testing.T) {
	resetProcessLocalRootForTest(t)
	rootDir := filepath.Join(t.TempDir(), "data")
	credentialPath := filepath.Join(t.TempDir(), "credentials", "control.token")
	t.Setenv("MIHARI_DATA", rootDir)
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", credentialPath)
	root, err := prepareLocalRoot()
	if err != nil {
		t.Fatal(err)
	}
	resources, err := openTUILogging(context.Background(), root.Paths, root.Token, root.FS, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root.Paths.TUILog); err != nil {
		t.Fatalf("TUI log=%q: %v", root.Paths.TUILog, err)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHelpCreatesNoDataRoot(t *testing.T) {
	resetProcessLocalRootForTest(t)
	rootDir := filepath.Join(t.TempDir(), "no-data")
	t.Setenv("MIHARI_DATA", rootDir)
	for _, args := range [][]string{{"--help"}, {"help"}, {"daemon", "--help"}, {"self", "version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetProcessLocalRootForTest(t)
			code := cli.Execute(context.Background(), args, io.Discard, io.Discard, productionLikeDependencies(t, nil))
			if code != cli.ExitOK {
				t.Fatalf("code=%d", code)
			}
			if _, err := os.Stat(rootDir); !os.IsNotExist(err) {
				t.Fatalf("data root created for %v: %v", args, err)
			}
			if localRootCached.FS != nil {
				t.Fatal("help/version cached a PrivateFS")
			}
		})
	}
}

func TestPrepareLocalRoot_StatusUsesLoadedToken(t *testing.T) {
	resetProcessLocalRootForTest(t)
	rootDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("MIHARI_DATA", rootDir)
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", "")
	token := strings.Repeat("ab", 32)
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "control.token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sawAuth = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schema":"mihari/v1","protocol_version":"v1","daemon_version":"dev","revision":1,"health":"ok","started_at":"1970-01-01T00:01:40Z"}`))
	}))
	t.Cleanup(server.Close)
	localClient := controlclient.NewHTTP(server.URL, "", server.Client())
	code := cli.Execute(context.Background(), []string{"status", "--json"}, io.Discard, io.Discard, cli.Dependencies{
		StatusClient: localClient,
		PrepareLocalRoot: func() error {
			root, err := prepareLocalRoot()
			if err != nil {
				return err
			}
			localClient.SetToken(root.Token)
			return nil
		},
	})
	if code != cli.ExitOK {
		t.Fatalf("code=%d", code)
	}
	if sawAuth != "Bearer "+token {
		t.Fatalf("authorization=%q", sawAuth)
	}
}

func TestPrepareLocalRoot_ExplicitCredentialTokenPropagatesToClientAndDaemon(t *testing.T) {
	for _, test := range []struct {
		name       string
		credential func(t *testing.T, cwd string) string
	}{
		{
			name: "relative credential",
			credential: func(_ *testing.T, _ string) string {
				return filepath.Join("credentials", "control.token")
			},
		},
		{
			name: "absolute credential",
			credential: func(t *testing.T, _ string) string {
				return filepath.Join(t.TempDir(), "control.token")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetProcessLocalRootForTest(t)
			cwd := t.TempDir()
			t.Chdir(cwd)
			t.Setenv("MIHARI_DATA", "data")
			configuredCredential := test.credential(t, cwd)
			t.Setenv("MIHARI_CONTROL_CREDENTIAL", configuredCredential)

			root, err := prepareLocalRoot()
			if err != nil {
				t.Fatal(err)
			}
			wantCredential, err := filepath.Abs(configuredCredential)
			if err != nil {
				t.Fatal(err)
			}
			if root.Token == "" {
				t.Fatal("explicit credential did not produce a token")
			}
			if root.Paths.Root != filepath.Join(cwd, "data") {
				t.Fatalf("data root=%q", root.Paths.Root)
			}
			if filepath.Clean(wantCredential) == root.Paths.ControlToken {
				t.Fatal("explicit credential unexpectedly used default control token path")
			}
			if raw, readErr := os.ReadFile(wantCredential); readErr != nil || strings.TrimSpace(string(raw)) != root.Token {
				t.Fatalf("credential=%q read=%v token=%q", wantCredential, readErr, root.Token)
			}

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if got := request.Header.Get("Authorization"); got != "Bearer "+root.Token {
					t.Fatalf("client bearer=%q", got)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"schema":"mihari/v1","protocol_version":"v1","daemon_version":"dev","revision":1,"health":"ok","started_at":"1970-01-01T00:01:40Z"}`))
			}))
			t.Cleanup(server.Close)
			localClient := controlclient.NewHTTP(server.URL, "", server.Client())
			if err := prepareLocalRootForClient(localClient)(); err != nil {
				t.Fatal(err)
			}
			if _, err := localClient.Status(context.Background()); err != nil {
				t.Fatal(err)
			}

			settings, holder := occupiedControllerSettings(t, strings.Repeat("e", 64))
			t.Cleanup(func() { _ = holder.Close() })
			writeSettings(t, root.Paths, settings)
			afterDaemonLoggingOpen = func(logger *slog.Logger) {
				logger.Info("prepared credential " + root.Token)
			}
			t.Cleanup(func() { afterDaemonLoggingOpen = nil })

			ctx, cancel := context.WithCancel(context.Background())
			ready := make(chan struct{})
			endpoint := transporttest.Endpoint(t)
			done := make(chan error, 1)
			go func() {
				done <- runDaemonWith(ctx, daemonRunDeps{
					Paths: root.Paths, PrivateFS: root.FS, Token: root.Token,
					Version: "test", Endpoint: endpoint, Ready: ready,
				})
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("runDaemonWith did not stop")
				}
			})
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("degraded daemon did not become ready")
			}
			logged := readFileString(t, root.Paths.DaemonLog)
			if strings.Contains(logged, root.Token) || !strings.Contains(logged, "***") {
				t.Fatalf("daemon log did not redact explicit credential: %s", logged)
			}
		})
	}
}

func TestPrepareLocalRoot_InteractiveRunTUIWithNilPrivateFS(t *testing.T) {
	resetProcessLocalRootForTest(t)
	rootDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("MIHARI_DATA", rootDir)
	newPrivateFS = func(string) (*platform.PrivateFS, error) {
		return nil, errors.New("injected fs failure")
	}
	var called bool
	var sawFS *platform.PrivateFS
	code := cli.Execute(context.Background(), []string{}, io.Discard, io.Discard, cli.Dependencies{
		Interactive: true,
		PrepareLocalRoot: func() error {
			root, err := prepareLocalRoot()
			if err != nil {
				return err
			}
			sawFS = root.FS
			return nil
		},
		RunTUI: func(context.Context) error {
			called = true
			return nil
		},
	})
	if code != cli.ExitOK || !called {
		t.Fatalf("code=%d called=%v", code, called)
	}
	if sawFS != nil {
		t.Fatal("expected RunTUI with PrivateFS=nil")
	}
}

type orderCloser struct {
	name  string
	order *[]string
	err   error
}

func (c orderCloser) Close() error {
	*c.order = append(*c.order, c.name)
	return c.err
}

type countingCloser struct {
	name  string
	order *[]string
	calls int
}

func (c *countingCloser) Close() error {
	c.calls++
	*c.order = append(*c.order, c.name)
	return nil
}

type countingCapture struct{ *countingCloser }

func (c *countingCapture) Write(value []byte) (int, error) { return len(value), nil }

func (c *countingCapture) Flush() error { return nil }

func resetProcessLocalRootForTest(t *testing.T) {
	t.Helper()
	resetProcessLocalRoot()
	t.Cleanup(func() {
		if localRootCached.FS != nil {
			_ = localRootCached.FS.Close()
		}
		resetProcessLocalRoot()
	})
}

func resetDaemonRunSeamsForTest(t *testing.T) {
	t.Helper()
	originalResources := newDaemonResources
	originalOpen := openDaemonRuntime
	originalCapture := newDaemonCapture
	originalBuild := buildDaemonRuntime
	originalRun := runDaemon
	t.Cleanup(func() {
		newDaemonResources = originalResources
		openDaemonRuntime = originalOpen
		newDaemonCapture = originalCapture
		buildDaemonRuntime = originalBuild
		runDaemon = originalRun
	})
}

func productionLikeDependencies(t *testing.T, runTUI func(context.Context) error) cli.Dependencies {
	t.Helper()
	localClient := controlclient.New("unused", "")
	return cli.Dependencies{
		StatusClient: localClient,
		PrepareLocalRoot: func() error {
			root, err := prepareLocalRoot()
			if err != nil {
				return err
			}
			localClient.SetToken(root.Token)
			return nil
		},
		RunTUI: runTUI,
	}
}

func absoluteTempPaths(t *testing.T) platform.Paths {
	t.Helper()
	paths, err := platform.NewPaths(filepath.Join(t.TempDir(), "data")).Absolute()
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func occupiedControllerSettings(t *testing.T, secret string) (config.Settings, net.Listener) {
	t.Helper()
	listeners := make([]net.Listener, 0, 3)
	addresses := make([]string, 0, 3)
	for range 3 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		addresses = append(addresses, listener.Addr().String())
	}
	if err := listeners[0].Close(); err != nil {
		t.Fatal(err)
	}
	if err := listeners[2].Close(); err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.MixedAddr = addresses[0]
	settings.ControllerAddr = addresses[1]
	settings.WebAddr = addresses[2]
	settings.ControllerSecret = secret
	return settings, listeners[1]
}

func writeSettings(t *testing.T, paths platform.Paths, settings config.Settings) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(paths.Settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(paths.Settings, settings); err != nil {
		t.Fatal(err)
	}
}

func writeCatalog(t *testing.T, paths platform.Paths, catalogURL string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(paths.SubscriptionCatalog), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "schema: mihari.subscriptions/v1\nglobal-interval: 12h\nprofiles:\n" +
		"  - id: 0123456789abcdef0123456789abcdef\n    name: probe\n    url: " + catalogURL + "\n    enabled: true\n"
	if err := os.WriteFile(paths.SubscriptionCatalog, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
