package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

type fakeController struct {
	installs   int
	uninstalls int
	starts     int
	stops      int
	restarts   int
	status     StatusKind
	statusErr  error
	controlErr error
	startErr   error
	stopErr    error
	events     *[]string
}

func (f *fakeController) record(event string) {
	if f.events != nil {
		*f.events = append(*f.events, event)
	}
}

func (f *fakeController) Install() error {
	f.record("install")
	f.installs++
	return f.controlErr
}
func (f *fakeController) Uninstall() error {
	f.record("uninstall")
	f.uninstalls++
	return f.controlErr
}
func (f *fakeController) Start() error {
	f.record("start")
	f.starts++
	if f.startErr != nil {
		return f.startErr
	}
	return f.controlErr
}
func (f *fakeController) Stop() error {
	f.record("stop")
	f.stops++
	if f.stopErr != nil {
		return f.stopErr
	}
	return f.controlErr
}
func (f *fakeController) Restart() error {
	f.record("restart")
	f.restarts++
	return f.controlErr
}
func (f *fakeController) Status() (StatusKind, error) {
	f.record("status")
	return f.status, f.statusErr
}
func (f *fakeController) Run() error { return nil }

func writeTempBinary(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestManagerInstallStartStopStatus(t *testing.T) {
	installRoot := t.TempDir()
	t.Setenv("MIHARI_INSTALL_ROOT", installRoot)
	src := writeTempBinary(t, t.TempDir(), "mihari-download.bin")
	fake := &fakeController{status: StatusRunning}
	var installExe string
	manager := New(Options{
		Executable: src,
		NewController: func(run RunFunc, executable string, arguments []string) (Controller, error) {
			if executable == "" || len(arguments) == 0 || arguments[0] != "daemon" {
				t.Fatalf("exe=%q args=%v", executable, arguments)
			}
			installExe = executable
			return fake, nil
		},
	})
	if err := manager.Install(); err != nil || fake.installs != 1 {
		t.Fatalf("install err=%v n=%d", err, fake.installs)
	}
	wantInstalled, err := platform.AbsoluteInstalledBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	if installExe != wantInstalled {
		t.Fatalf("service ImagePath=%q want installed %q (not download path %q)", installExe, wantInstalled, src)
	}
	if _, err := os.Stat(wantInstalled); err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	if err := manager.Start(); err != nil || fake.starts != 1 {
		t.Fatal(err)
	}
	st, err := manager.Status()
	if err != nil || st != StatusRunning {
		t.Fatalf("status=%q err=%v", st, err)
	}
	if err := manager.Stop(); err != nil || fake.stops != 1 {
		t.Fatal(err)
	}
	if err := manager.Restart(); err != nil || fake.restarts != 1 {
		t.Fatal(err)
	}
	// Uninstall stops first, then deletes registration.
	if err := manager.Uninstall(); err != nil || fake.uninstalls != 1 {
		t.Fatal(err)
	}
	if fake.stops != 2 {
		t.Fatalf("uninstall should stop first: stops=%d", fake.stops)
	}
}

func TestManagerUpdateInstalledBinaryCopiesBeforeRestart(t *testing.T) {
	installRoot := t.TempDir()
	t.Setenv("MIHARI_INSTALL_ROOT", installRoot)
	source := writeTempBinary(t, t.TempDir(), "mihari-new")
	installed, err := platform.AbsoluteInstalledBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	events := []string{}
	fake := &fakeController{status: StatusRunning, events: &events}
	manager := New(Options{
		Executable: source,
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})
	manager.stageBinary = func(path string) (string, error) {
		events = append(events, "sync")
		return platform.StageInstalledBinary(path)
	}

	updated, err := manager.UpdateInstalledBinary()
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	if got, want := strings.Join(events, ","), "status,stop,sync,start"; got != want {
		t.Fatalf("events=%q want=%q", got, want)
	}
	payload, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(payload), "payload"; got != want {
		t.Fatalf("installed payload=%q want=%q", got, want)
	}
}

func TestManagerUpdateInstalledBinarySkipsMissingService(t *testing.T) {
	t.Setenv("MIHARI_INSTALL_ROOT", t.TempDir())
	events := []string{}
	fake := &fakeController{status: StatusNotInstalled, events: &events}
	manager := New(Options{
		Executable: writeTempBinary(t, t.TempDir(), "mihari-new"),
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})
	manager.stageBinary = func(string) (string, error) {
		events = append(events, "sync")
		return "", nil
	}

	updated, err := manager.UpdateInstalledBinary()
	if err != nil || updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	if got, want := strings.Join(events, ","), "status"; got != want {
		t.Fatalf("events=%q want=%q", got, want)
	}
}

func TestManagerUpdateInstalledBinaryStatusFailureIsRedacted(t *testing.T) {
	events := []string{}
	fake := &fakeController{
		statusErr: errors.New(`query C:\Users\secret\mihari.exe: failed`),
		events:    &events,
	}
	manager := New(Options{
		Executable: "mihari-new",
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})

	installed, err := manager.UpdateInstalledBinary()
	if installed || err == nil {
		t.Fatalf("installed=%v err=%v", installed, err)
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(api.Message, "secret") || strings.Contains(api.Message, `C:\Users`) {
		t.Fatalf("unsafe message=%q", api.Message)
	}
	if got, want := strings.Join(events, ","), "status"; got != want {
		t.Fatalf("events=%q want=%q", got, want)
	}
}

func TestManagerUpdateInstalledBinarySamePathDoesNotCorruptBinary(t *testing.T) {
	installRoot := t.TempDir()
	t.Setenv("MIHARI_INSTALL_ROOT", installRoot)
	installed, err := platform.AbsoluteInstalledBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("new-version"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeController{status: StatusRunning}
	manager := New(Options{
		Executable: installed,
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})

	updated, err := manager.UpdateInstalledBinary()
	if err != nil || !updated {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	payload, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(payload), "new-version"; got != want {
		t.Fatalf("installed payload=%q want=%q", got, want)
	}
	if fake.stops != 1 || fake.starts != 1 {
		t.Fatalf("stops=%d starts=%d", fake.stops, fake.starts)
	}
}

func TestManagerUpdateInstalledBinarySyncFailureRestartsPreviousService(t *testing.T) {
	raw := errors.New(`copy C:\Users\secret\mihari.exe: access denied`)
	events := []string{}
	fake := &fakeController{status: StatusRunning, events: &events}
	manager := New(Options{
		Executable: "mihari-new",
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})
	manager.stageBinary = func(string) (string, error) {
		events = append(events, "sync")
		return "", raw
	}

	updated, err := manager.UpdateInstalledBinary()
	if !updated || err == nil {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(api.Message, "secret") || strings.Contains(api.Message, "access denied") {
		t.Fatalf("unsafe message=%q", api.Message)
	}
	if got, want := strings.Join(events, ","), "status,stop,sync,start"; got != want {
		t.Fatalf("events=%q want=%q", got, want)
	}
}

func TestManagerUpdateInstalledBinaryStopFailureDoesNotTouchInstalledCopy(t *testing.T) {
	raw := errors.New(`stop C:\Users\secret\mihari.exe: access denied`)
	events := []string{}
	fake := &fakeController{status: StatusRunning, stopErr: raw, events: &events}
	manager := New(Options{
		Executable: "mihari-new",
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})
	manager.stageBinary = func(string) (string, error) {
		events = append(events, "sync")
		return "", nil
	}

	installed, err := manager.UpdateInstalledBinary()
	if !installed || err == nil {
		t.Fatalf("installed=%v err=%v", installed, err)
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(api.Message, "secret") || strings.Contains(api.Message, "access denied") {
		t.Fatalf("unsafe message=%q", api.Message)
	}
	if got, want := strings.Join(events, ","), "status,stop"; got != want {
		t.Fatalf("events=%q want=%q", got, want)
	}
}

func TestManagerUpdateInstalledBinaryStartFailureReportsSynchronizedPartialSuccess(t *testing.T) {
	raw := errors.New(`start C:\Users\secret\mihari.exe: timed out`)
	events := []string{}
	fake := &fakeController{status: StatusRunning, startErr: raw, events: &events}
	manager := New(Options{
		Executable: "mihari-new",
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})
	manager.stageBinary = func(string) (string, error) {
		events = append(events, "sync")
		return "installed-mihari", nil
	}

	installed, err := manager.UpdateInstalledBinary()
	if !installed || err == nil {
		t.Fatalf("installed=%v err=%v", installed, err)
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState || !strings.Contains(api.Message, "synchronized") {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(api.Message, "secret") || strings.Contains(api.Message, "timed out") {
		t.Fatalf("unsafe message=%q", api.Message)
	}
	if got, want := strings.Join(events, ","), "status,stop,sync,start"; got != want {
		t.Fatalf("events=%q want=%q", got, want)
	}
}

func TestManagerUpdateInstalledBinarySyncAndRecoveryFailureReportsBoth(t *testing.T) {
	events := []string{}
	fake := &fakeController{status: StatusRunning, startErr: errors.New("recovery failed"), events: &events}
	manager := New(Options{
		Executable: "mihari-new",
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})
	manager.stageBinary = func(string) (string, error) {
		events = append(events, "sync")
		return "", errors.New("sync failed")
	}

	installed, err := manager.UpdateInstalledBinary()
	if !installed || err == nil {
		t.Fatalf("installed=%v err=%v", installed, err)
	}
	var api protocol.APIError
	if !errors.As(err, &api) || !strings.Contains(api.Message, "synchronization failed") || !strings.Contains(api.Message, "previous service") {
		t.Fatalf("err=%v", err)
	}
	if got, want := strings.Join(events, ","), "status,stop,sync,start"; got != want {
		t.Fatalf("events=%q want=%q", got, want)
	}
}

func TestManagerUninstallStopsBeforeDelete(t *testing.T) {
	t.Setenv("MIHARI_INSTALL_ROOT", t.TempDir())
	src := writeTempBinary(t, t.TempDir(), "mihari-download.bin")
	fake := &fakeController{status: StatusRunning}
	manager := New(Options{
		Executable: src,
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})
	if err := manager.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if fake.stops != 1 || fake.uninstalls != 1 {
		t.Fatalf("stops=%d uninstalls=%d want both 1", fake.stops, fake.uninstalls)
	}
}

func TestManagerReinstallStopUninstallInstallStart(t *testing.T) {
	t.Setenv("MIHARI_INSTALL_ROOT", t.TempDir())
	src := writeTempBinary(t, t.TempDir(), "mihari-download.bin")
	fake := &fakeController{status: StatusRunning}
	manager := New(Options{
		Executable: src,
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})
	if err := manager.Reinstall(); err != nil {
		t.Fatal(err)
	}
	// Uninstall path stops once; reinstall does not add an extra stop before that.
	if fake.stops != 1 || fake.uninstalls != 1 || fake.installs != 1 || fake.starts != 1 {
		t.Fatalf("stops=%d uninstalls=%d installs=%d starts=%d", fake.stops, fake.uninstalls, fake.installs, fake.starts)
	}
}

func TestManagerReinstallWhenNotInstalled(t *testing.T) {
	t.Setenv("MIHARI_INSTALL_ROOT", t.TempDir())
	src := writeTempBinary(t, t.TempDir(), "mihari-download.bin")
	// Stop/Uninstall report not-installed; Install/Start must still succeed.
	step := &reinstallFake{}
	manager := New(Options{
		Executable: src,
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return step, nil
		},
	})
	if err := manager.Reinstall(); err != nil {
		t.Fatal(err)
	}
	if step.installs != 1 || step.starts != 1 {
		t.Fatalf("installs=%d starts=%d", step.installs, step.starts)
	}
}

type reinstallFake struct {
	installs, starts, stops, uninstalls int
}

func (f *reinstallFake) Install() error {
	f.installs++
	return nil
}
func (f *reinstallFake) Uninstall() error {
	f.uninstalls++
	return errors.New("service mihari is not installed")
}
func (f *reinstallFake) Start() error {
	f.starts++
	return nil
}
func (f *reinstallFake) Stop() error {
	f.stops++
	return errors.New("service mihari is not installed")
}
func (f *reinstallFake) Restart() error              { return nil }
func (f *reinstallFake) Status() (StatusKind, error) { return StatusNotInstalled, nil }
func (f *reinstallFake) Run() error                  { return nil }

func TestManagerMapsNotInstalled(t *testing.T) {
	fake := &fakeController{controlErr: errors.New("service mihari is not installed")}
	manager := New(Options{
		NewController: func(RunFunc, string, []string) (Controller, error) { return fake, nil },
	})
	err := manager.Start()
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("err=%v", err)
	}
}

func TestManagerStatusNotInstalledIsNilError(t *testing.T) {
	fake := &fakeController{statusErr: errors.New("the specified service does not exist as an installed service")}
	manager := New(Options{
		NewController: func(RunFunc, string, []string) (Controller, error) { return fake, nil },
	})
	st, err := manager.Status()
	if err != nil {
		t.Fatalf("Status() err=%v, want nil for not-installed", err)
	}
	if st != StatusNotInstalled {
		t.Fatalf("status=%q want %q", st, StatusNotInstalled)
	}
}

func TestManagerStatusAccessDeniedFallsBackToPlatformQuery(t *testing.T) {
	// Simulate kardianos Access is denied; without a working platform fallback
	// on non-Windows this returns mapped permission error — on Windows
	// platformQueryStatus may still succeed for a real local service.
	fake := &fakeController{statusErr: errors.New("Access is denied.")}
	manager := New(Options{
		NewController: func(RunFunc, string, []string) (Controller, error) { return fake, nil },
	})
	st, err := manager.Status()
	// Either platform fallback succeeds (any StatusKind, nil err) or permission error.
	if err == nil {
		switch st {
		case StatusRunning, StatusStopped, StatusNotInstalled, StatusUnknown:
			return
		default:
			t.Fatalf("unexpected status %q", st)
		}
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodePermissionDenied {
		t.Fatalf("err=%v (status=%q)", err, st)
	}
}

func TestManagerRunRequiresFunc(t *testing.T) {
	manager := New(Options{
		NewController: func(RunFunc, string, []string) (Controller, error) { return &fakeController{}, nil },
	})
	err := manager.Run()
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument {
		t.Fatalf("err=%v", err)
	}
}

func TestMapServiceErrorStartTimeout(t *testing.T) {
	err := mapServiceError(errors.New("Failed to start Mihari: The service did not respond to the start or control request in a timely fashion."))
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(api.Message, "failed to start in time") {
		t.Fatalf("message=%q", api.Message)
	}
}

func TestMapServiceErrorPermissionAndMarkedForDeletion(t *testing.T) {
	err := mapServiceError(errors.New("Access is denied."))
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodePermissionDenied {
		t.Fatalf("permission err=%v", err)
	}

	err = mapServiceError(errors.New("The specified service has been marked for deletion. (1072)"))
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("marked-for-deletion err=%v", err)
	}
	if !strings.Contains(strings.ToLower(api.Message), "marked for deletion") {
		t.Fatalf("message=%q", api.Message)
	}
}

func TestIsIgnorableStopError(t *testing.T) {
	if !isIgnorableStopError(errors.New("service has not been started")) {
		t.Fatal("expected not-started to be ignorable")
	}
	if !isIgnorableStopError(errors.New("service mihari is not installed")) {
		t.Fatal("expected not-installed to be ignorable")
	}
	if isIgnorableStopError(errors.New("access is denied")) {
		t.Fatal("permission errors must not be ignored")
	}
}

func TestInstallEnvVarsPinsAbsoluteMIHARI_DATA(t *testing.T) {
	root := filepath.Join(t.TempDir(), "service-data")
	t.Setenv("MIHARI_DATA", root)
	env := installEnvVars()
	got, ok := env["MIHARI_DATA"]
	if !ok || got == "" {
		t.Fatalf("env=%v", env)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("MIHARI_DATA not absolute: %q", got)
	}
	if got != filepath.Clean(root) && got != root {
		// AbsoluteDataRoot cleans; accept either equal after Abs.
		want, err := filepath.Abs(root)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("MIHARI_DATA=%q want=%q", got, want)
		}
	}
}

func TestProgramStartStopCancelsRun(t *testing.T) {
	started := make(chan struct{})
	p := &program{run: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	}}
	if err := p.Start(nil); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := p.Stop(nil); err != nil {
		t.Fatal(err)
	}
}

func TestProgramStartReturnsRunErrorBeforeReady(t *testing.T) {
	want := errors.New("listen failed")
	ready := make(chan struct{})
	p := &program{run: func(context.Context) error { return want }, ready: ready, startTimeout: time.Second}
	if err := p.Start(nil); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestProgramStartSucceedsAfterReady(t *testing.T) {
	ready := make(chan struct{})
	started := make(chan struct{})
	p := &program{
		run: func(ctx context.Context) error {
			close(ready)
			close(started)
			<-ctx.Done()
			return nil
		},
		ready:        ready,
		startTimeout: time.Second,
	}
	if err := p.Start(nil); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := p.Stop(nil); err != nil {
		t.Fatal(err)
	}
}

func TestProgramStartWithoutReadyReturnsImmediately(t *testing.T) {
	p := &program{run: func(context.Context) error { return errors.New("later") }}
	if err := p.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(nil); err != nil {
		t.Fatal(err)
	}
}
