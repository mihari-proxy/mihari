package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/service"
)

type fakeService struct {
	installs   int
	reinstalls int
	starts     int
	status     service.StatusKind
}

type fakePurgeUninstaller struct {
	calls int
	err   error
}

func (*fakePurgeUninstaller) Preview(context.Context) ([]app.UninstallTarget, error) { return nil, nil }

func (f *fakePurgeUninstaller) Run(_ context.Context, progress func(string)) error {
	f.calls++
	progress("Uninstalling Mihari service")
	return f.err
}

type orderedPurgeUninstaller struct {
	order *[]string
	calls int
}

func (*orderedPurgeUninstaller) Preview(context.Context) ([]app.UninstallTarget, error) {
	return nil, nil
}

func (f *orderedPurgeUninstaller) Run(context.Context, func(string)) error {
	f.calls++
	*f.order = append(*f.order, "run")
	return nil
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

func TestServiceUninstall_PurgeRequiresYesBeforeAction(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	calls := 0
	stderr := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--purge"}, io.Discard, stderr, Dependencies{
		ServiceAction: func(context.Context, string) error {
			calls++
			return nil
		},
	})
	if exit != ExitUsage || calls != 0 || !strings.Contains(stderr.String(), "--yes") {
		t.Fatalf("exit=%d calls=%d stderr=%q", exit, calls, stderr.String())
	}
}

func TestServiceUninstall_WithoutPurgeKeepsExistingAction(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	var action string
	exit := Execute(context.Background(), []string{"service", "uninstall", "--json"}, io.Discard, &bytes.Buffer{}, Dependencies{
		ServiceAction: func(_ context.Context, got string) error {
			action = got
			return nil
		},
	})
	if exit != ExitOK || action != "uninstall" {
		t.Fatalf("exit=%d action=%q", exit, action)
	}
}

func TestServiceUninstall_PurgeWithYesRunsOnceAndKeepsJSONSingleEnvelope(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	for _, jsonOutput := range []bool{false, true} {
		t.Run(strconv.FormatBool(jsonOutput), func(t *testing.T) {
			uninstaller := &fakePurgeUninstaller{}
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			args := []string{"service", "uninstall", "--purge", "--yes"}
			if jsonOutput {
				args = append(args, "--json")
			}
			exit := Execute(context.Background(), args, stdout, stderr, Dependencies{Uninstaller: uninstaller})
			if exit != ExitOK || uninstaller.calls != 1 {
				t.Fatalf("exit=%d calls=%d stdout=%q stderr=%q", exit, uninstaller.calls, stdout.String(), stderr.String())
			}
			if jsonOutput {
				if strings.Count(stdout.String(), "\n") != 1 || !strings.Contains(stdout.String(), `"ok":true`) || stderr.Len() != 0 {
					t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
				return
			}
			if !strings.Contains(stderr.String(), "Uninstalling Mihari service") || !strings.Contains(stdout.String(), "Mihari has been completely uninstalled") {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestServiceUninstall_PurgeFailurePreservesEnglishError(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	stderr := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--purge", "--yes"}, io.Discard, stderr, Dependencies{
		Uninstaller: &fakePurgeUninstaller{err: errors.New("Mihari did not stop within 30 seconds; stop it and retry the uninstall")},
	})
	if exit != ExitInvalidState || !strings.Contains(stderr.String(), "Mihari did not stop within 30 seconds") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestServiceUninstall_PurgeClosesLocalResourcesBeforeRun(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	var order []string
	uninstaller := &orderedPurgeUninstaller{order: &order}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--purge", "--yes"}, io.Discard, io.Discard, Dependencies{
		Uninstaller: uninstaller,
		CloseForPurgeUninstall: func() error {
			order = append(order, "close")
			return nil
		},
	})
	if exit != ExitOK || uninstaller.calls != 1 || len(order) != 2 || order[0] != "close" || order[1] != "run" {
		t.Fatalf("exit=%d calls=%d order=%v", exit, uninstaller.calls, order)
	}
}

func TestServiceUninstall_PurgeCloseFailurePreventsRun(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	uninstaller := &fakePurgeUninstaller{}
	stderr := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--purge", "--yes"}, io.Discard, stderr, Dependencies{
		Uninstaller:            uninstaller,
		CloseForPurgeUninstall: func() error { return errors.New("close failed") },
	})
	if exit != ExitInvalidState || uninstaller.calls != 0 || !strings.Contains(stderr.String(), "close Mihari local resources before uninstall") {
		t.Fatalf("exit=%d calls=%d stderr=%q", exit, uninstaller.calls, stderr.String())
	}
}
