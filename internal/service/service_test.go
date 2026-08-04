package service

import (
	"context"
	"errors"
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
