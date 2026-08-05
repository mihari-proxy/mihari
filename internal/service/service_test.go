package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
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
}

func (f *fakeController) Install() error {
	f.installs++
	return f.controlErr
}
func (f *fakeController) Uninstall() error {
	f.uninstalls++
	return f.controlErr
}
func (f *fakeController) Start() error {
	f.starts++
	return f.controlErr
}
func (f *fakeController) Stop() error {
	f.stops++
	return f.controlErr
}
func (f *fakeController) Restart() error {
	f.restarts++
	return f.controlErr
}
func (f *fakeController) Status() (StatusKind, error) { return f.status, f.statusErr }
func (f *fakeController) Run() error                  { return nil }

func TestManagerInstallStartStopStatus(t *testing.T) {
	fake := &fakeController{status: StatusRunning}
	manager := New(Options{
		Executable: "C:\\fake\\mihari.exe",
		NewController: func(run RunFunc, executable string, arguments []string) (Controller, error) {
			if executable == "" || len(arguments) == 0 || arguments[0] != "daemon" {
				t.Fatalf("exe=%q args=%v", executable, arguments)
			}
			return fake, nil
		},
	})
	if err := manager.Install(); err != nil || fake.installs != 1 {
		t.Fatalf("install err=%v n=%d", err, fake.installs)
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
	if err := manager.Uninstall(); err != nil || fake.uninstalls != 1 {
		t.Fatal(err)
	}
}

func TestManagerReinstallStopUninstallInstallStart(t *testing.T) {
	fake := &fakeController{status: StatusRunning}
	manager := New(Options{
		Executable: "C:\\fake\\mihari.exe",
		NewController: func(RunFunc, string, []string) (Controller, error) {
			return fake, nil
		},
	})
	if err := manager.Reinstall(); err != nil {
		t.Fatal(err)
	}
	if fake.stops != 1 || fake.uninstalls != 1 || fake.installs != 1 || fake.starts != 1 {
		t.Fatalf("stops=%d uninstalls=%d installs=%d starts=%d", fake.stops, fake.uninstalls, fake.installs, fake.starts)
	}
}

func TestManagerReinstallWhenNotInstalled(t *testing.T) {
	// Stop/Uninstall report not-installed; Install/Start must still succeed.
	step := &reinstallFake{}
	manager := New(Options{
		Executable: "C:\\fake\\mihari.exe",
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
