package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
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
	calls        int
	forceCalls   int
	err          error
	consent      app.UninstallCommandConsent
	unmatched    app.UnmatchedCommandFile
	hasUnmatched bool
}

func (*fakePurgeUninstaller) Preview(context.Context) ([]app.UninstallTarget, error) { return nil, nil }

func (f *fakePurgeUninstaller) Run(_ context.Context, progress func(string)) error {
	f.calls++
	return f.finish(progress)
}

func (f *fakePurgeUninstaller) RunForce(_ context.Context, progress func(string)) error {
	f.forceCalls++
	return f.finish(progress)
}

func (f *fakePurgeUninstaller) finish(progress func(string)) error {
	if progress != nil {
		progress("Uninstalling Mihari service")
	}
	if f.hasUnmatched && !f.consent.DeleteUnmatched {
		path := f.unmatched.Path
		if path == "" {
			path = "command file"
		}
		return fmt.Errorf("leaving %s: command file does not match the installed Mihari program", path)
	}
	return f.err
}

func (f *fakePurgeUninstaller) UnmatchedCommand(context.Context) (app.UnmatchedCommandFile, bool, error) {
	return f.unmatched, f.hasUnmatched, nil
}

func (f *fakePurgeUninstaller) RunWithCommandConsent(_ context.Context, progress func(string), consent app.UninstallCommandConsent) error {
	f.consent = consent
	return f.Run(context.Background(), progress)
}

func (f *fakePurgeUninstaller) RunForceWithCommandConsent(_ context.Context, progress func(string), consent app.UninstallCommandConsent) error {
	f.consent = consent
	return f.RunForce(context.Background(), progress)
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

func (f *orderedPurgeUninstaller) RunForce(context.Context, func(string)) error {
	f.calls++
	*f.order = append(*f.order, "run-force")
	return nil
}

func (*orderedPurgeUninstaller) UnmatchedCommand(context.Context) (app.UnmatchedCommandFile, bool, error) {
	return app.UnmatchedCommandFile{}, false, nil
}

func (f *orderedPurgeUninstaller) RunWithCommandConsent(ctx context.Context, progress func(string), _ app.UninstallCommandConsent) error {
	return f.Run(ctx, progress)
}

func (f *orderedPurgeUninstaller) RunForceWithCommandConsent(ctx context.Context, progress func(string), _ app.UninstallCommandConsent) error {
	return f.RunForce(ctx, progress)
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
			if exit != ExitOK || uninstaller.calls != 1 || uninstaller.forceCalls != 0 {
				t.Fatalf("exit=%d calls=%d forceCalls=%d stdout=%q stderr=%q", exit, uninstaller.calls, uninstaller.forceCalls, stdout.String(), stderr.String())
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
		Uninstaller: &fakePurgeUninstaller{err: errors.New("did not stop within 30 seconds; stop Mihari and retry the uninstall")},
	})
	if exit != ExitInvalidState || !strings.Contains(stderr.String(), "did not stop within 30 seconds") {
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

func TestServiceUninstall_ForceRequiresPurge(t *testing.T) {
	stderr := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--force"}, io.Discard, stderr, Dependencies{})
	if exit != ExitUsage || !strings.Contains(stderr.String(), "--force requires --purge") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestServiceUninstall_PurgeForceRequiresYes(t *testing.T) {
	stderr := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--purge", "--force"}, io.Discard, stderr, Dependencies{})
	if exit != ExitUsage || !strings.Contains(stderr.String(), "--purge requires --yes") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestServiceUninstall_PurgeYesForceRunsForceOnce(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	uninstaller := &fakePurgeUninstaller{}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--purge", "--yes", "--force"}, stdout, stderr, Dependencies{Uninstaller: uninstaller})
	if exit != ExitOK || uninstaller.calls != 0 || uninstaller.forceCalls != 1 {
		t.Fatalf("exit=%d calls=%d forceCalls=%d stdout=%q stderr=%q", exit, uninstaller.calls, uninstaller.forceCalls, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Uninstalling Mihari service") || !strings.Contains(stdout.String(), "Mihari has been completely uninstalled") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestServiceUninstall_DeleteUnmatchedCommandRequiresPurge(t *testing.T) {
	stderr := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--delete-unmatched-command"}, io.Discard, stderr, Dependencies{})
	if exit != ExitUsage || !strings.Contains(stderr.String(), "--delete-unmatched-command requires --purge") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestServiceUninstall_DeleteUnmatchedCommandFlagConsentsWithoutPrompt(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	uninstaller := &fakePurgeUninstaller{hasUnmatched: true, unmatched: app.UnmatchedCommandFile{Path: "/usr/local/bin/mihari", Reason: "does not match"}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--purge", "--yes", "--delete-unmatched-command"}, stdout, stderr, Dependencies{Interactive: true, Uninstaller: uninstaller})
	if exit != ExitOK || !uninstaller.consent.DeleteUnmatched || strings.Contains(stderr.String(), "Delete unmatched command file") {
		t.Fatalf("exit=%d consent=%v stdout=%q stderr=%q", exit, uninstaller.consent, stdout.String(), stderr.String())
	}
}

func TestServiceUninstall_PurgeYesWithoutTerminalLeavesUnmatchedCommand(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	uninstaller := &fakePurgeUninstaller{hasUnmatched: true, unmatched: app.UnmatchedCommandFile{Path: "/usr/local/bin/mihari"}}
	stderr := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "uninstall", "--purge", "--yes"}, io.Discard, stderr, Dependencies{Uninstaller: uninstaller})
	if exit != ExitInvalidState || uninstaller.consent.DeleteUnmatched || uninstaller.calls != 1 || strings.Contains(stderr.String(), "Delete unmatched command file") || !strings.Contains(stderr.String(), "leaving /usr/local/bin/mihari") {
		t.Fatalf("exit=%d consent=%v calls=%d stderr=%q", exit, uninstaller.consent, uninstaller.calls, stderr.String())
	}
}

func TestServiceUninstall_JSONAndNonInteractiveDoNotPrompt(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	for _, test := range []struct {
		name string
		args []string
		deps Dependencies
	}{
		{name: "json", args: []string{"service", "uninstall", "--purge", "--yes", "--json"}, deps: Dependencies{Interactive: true}},
		{name: "non-interactive", args: []string{"service", "uninstall", "--purge", "--yes"}, deps: Dependencies{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			uninstaller := &fakePurgeUninstaller{hasUnmatched: true, unmatched: app.UnmatchedCommandFile{Path: "/usr/local/bin/mihari"}}
			test.deps.Uninstaller = uninstaller
			exit, stderr, _ := executeUninstallCommand(t, test.args, strings.NewReader("y\n"), test.deps)
			if exit != ExitInvalidState || uninstaller.consent.DeleteUnmatched || strings.Contains(stderr, "Delete unmatched command file") {
				t.Fatalf("exit=%d consent=%v stderr=%q", exit, uninstaller.consent, stderr)
			}
		})
	}
}

func TestServiceUninstall_PromptReadErrorStopsBeforeRemoval(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	uninstaller := &fakePurgeUninstaller{hasUnmatched: true, unmatched: app.UnmatchedCommandFile{Path: "/usr/local/bin/mihari"}}
	exit, _, err := executeUninstallCommand(t, []string{"service", "uninstall", "--purge", "--yes"}, uninstallReadError{}, Dependencies{Interactive: true, Uninstaller: uninstaller})
	var api protocol.APIError
	if exit != ExitUsage || uninstaller.calls != 0 || !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument || api.Message != "could not read unmatched command confirmation" {
		t.Fatalf("exit=%d calls=%d err=%v", exit, uninstaller.calls, err)
	}
}

func executeUninstallCommand(t *testing.T, args []string, in io.Reader, deps Dependencies) (int, string, error) {
	t.Helper()
	stderr := &bytes.Buffer{}
	cmd := newRoot(deps, &runOptions{})
	cmd.SetArgs(args)
	cmd.SetIn(in)
	cmd.SetOut(io.Discard)
	cmd.SetErr(stderr)
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		return ExitOK, stderr.String(), nil
	}
	return exitCode(err), stderr.String(), err
}

func TestConfirmDeleteUnmatchedCommand_ReadError(t *testing.T) {
	_, err := confirmDeleteUnmatchedCommand(uninstallReadError{}, io.Discard, "/usr/local/bin/mihari")
	if err == nil {
		t.Fatal("read error was treated as a decline")
	}
}

func TestConfirmDeleteUnmatchedCommand_EscapesControlCharacters(t *testing.T) {
	stderr := &bytes.Buffer{}
	accepted, err := confirmDeleteUnmatchedCommand(strings.NewReader("n\n"), stderr, "/tmp/\x1bmihari")
	if err != nil || accepted || strings.Contains(stderr.String(), "\x1b") || !strings.Contains(stderr.String(), `\x1b`) {
		t.Fatalf("accepted=%t err=%v stderr=%q", accepted, err, stderr.String())
	}
}

type uninstallReadError struct{}

func (uninstallReadError) Read([]byte) (int, error) { return 0, errors.New("stdin failed") }

func TestServiceUninstall_TerminalPromptConsentsOnlyToYes(t *testing.T) {
	prev := elevate.Check
	t.Cleanup(func() { elevate.Check = prev })
	elevate.Check = func() bool { return true }
	for _, answer := range []struct {
		input string
		want  bool
		exit  int
	}{{"y\n", true, ExitOK}, {"YES\n", true, ExitOK}, {"n\n", false, ExitInvalidState}, {"\n", false, ExitInvalidState}} {
		t.Run(answer.input, func(t *testing.T) {
			uninstaller := &fakePurgeUninstaller{hasUnmatched: true, unmatched: app.UnmatchedCommandFile{Path: "/usr/local/bin/mihari", Reason: "missing"}}
			exit, stderr, _ := executeUninstallCommand(t, []string{"service", "uninstall", "--purge", "--yes"}, strings.NewReader(answer.input), Dependencies{Interactive: true, Uninstaller: uninstaller})
			if exit != answer.exit || uninstaller.consent.DeleteUnmatched != answer.want || !strings.Contains(stderr, "Delete unmatched command file /usr/local/bin/mihari? [y/N]") {
				t.Fatalf("exit=%d consent=%v stderr=%q", exit, uninstaller.consent, stderr)
			}
		})
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
