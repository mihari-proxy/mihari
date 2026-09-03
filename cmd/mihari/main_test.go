package main

import (
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

	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestTUIRelaunchArgsStartsDefaultTUI(t *testing.T) {
	binary := `C:\Program Files\Mihari\mihari.exe`
	if got, want := tuiRelaunchArgs(binary), []string{binary}; !slices.Equal(got, want) {
		t.Fatalf("args=%q want=%q", got, want)
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
	done := make(chan error, 1)
	go func() {
		done <- runDaemonWith(ctx, daemonRunDeps{
			Paths:     paths,
			PrivateFS: fs,
			Token:     token,
			Version:   "test",
			Endpoint:  transporttest.Endpoint(t),
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
	done := make(chan error, 1)
	go func() {
		done <- runDaemonWith(ctx, daemonRunDeps{
			Paths:     paths,
			PrivateFS: fs,
			Token:     "catalog-fail-token",
			Version:   "test",
			Endpoint:  transporttest.Endpoint(t),
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
