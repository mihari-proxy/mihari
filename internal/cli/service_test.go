package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/service"
)

type fakeService struct {
	installs   int
	reinstalls int
	starts     int
	status     service.StatusKind
}

func (f *fakeService) Install() error                      { f.installs++; return nil }
func (f *fakeService) Uninstall() error                    { return nil }
func (f *fakeService) Reinstall() error                    { f.reinstalls++; return nil }
func (f *fakeService) Start() error                        { f.starts++; return nil }
func (f *fakeService) Stop() error                         { return nil }
func (f *fakeService) Restart() error                      { return nil }
func (f *fakeService) Status() (service.StatusKind, error) { return f.status, nil }

func TestServiceInstallRequiresElevation(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return false }
	fake := &fakeService{}
	exit := Execute(context.Background(), []string{"service", "install", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{ServiceController: fake})
	if exit != ExitPermission || fake.installs != 0 {
		t.Fatalf("exit=%d installs=%d", exit, fake.installs)
	}
}

func TestServiceInstallWhenElevated(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	fake := &fakeService{}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "install", "--json"}, stdout, stderr, Dependencies{ServiceController: fake})
	if exit != ExitOK || fake.installs != 1 || !strings.Contains(stdout.String(), `"ok":true`) {
		t.Fatalf("exit=%d stdout=%q stderr=%q installs=%d", exit, stdout, stderr, fake.installs)
	}
}

func TestServiceStatusJSON(t *testing.T) {
	fake := &fakeService{status: service.StatusRunning}
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "status", "--json"}, stdout, &bytes.Buffer{}, Dependencies{ServiceController: fake})
	if exit != ExitOK || !strings.Contains(stdout.String(), `"status":"running"`) {
		t.Fatalf("exit=%d stdout=%q", exit, stdout)
	}
}

func TestServiceReinstallWhenElevated(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	fake := &fakeService{}
	stdout := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "reinstall", "--json"}, stdout, &bytes.Buffer{}, Dependencies{ServiceController: fake})
	if exit != ExitOK || fake.reinstalls != 1 || !strings.Contains(stdout.String(), `"action":"reinstall"`) {
		t.Fatalf("exit=%d reinstalls=%d stdout=%q", exit, fake.reinstalls, stdout)
	}
}

func TestServiceAction_ForwardsCommandContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	deps := Dependencies{ServiceAction: func(got context.Context, op string) error {
		called = true
		if op != "stop" || got.Err() != context.Canceled {
			t.Fatal("lost lifecycle operation/context")
		}
		return nil
	}}
	cmd := newServiceActionCommand("stop", "", deps, &runOptions{}, false, func(ServiceController) error { t.Fatal("legacy service action"); return nil })
	cmd.SetContext(ctx)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("transactional service callback not called")
	}
}
