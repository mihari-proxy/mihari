package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDaemon_LaunchdProcessGroupUsesDedicatedGate(t *testing.T) {
	var out bytes.Buffer
	calls := 0
	deps := Dependencies{
		RunDaemon:               func(context.Context) error { t.Fatal("launchd used foreground bootstrap"); return nil },
		RunSystemServiceDaemon:  func(context.Context) error { t.Fatal("launchd lost shared group mode"); return nil },
		RunLaunchdServiceDaemon: func(context.Context) error { calls++; return nil },
	}
	code := Execute(context.Background(), []string{"daemon", "--system-service", "--launchd-process-group"}, &out, &out, deps)
	if code != ExitOK || calls != 1 {
		t.Fatalf("launchd gate calls=%d code=%d out=%s", calls, code, out.String())
	}
}

func TestDaemon_LaunchdProcessGroupRejectsInvalidModes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		capability bool
	}{
		{"missing-system-service", []string{"--launchd-process-group"}, true},
		{"false-system-service", []string{"--system-service=false", "--launchd-process-group"}, true},
		{"false-launchd", []string{"--system-service", "--launchd-process-group=false"}, true},
		{"false-launchd-foreground", []string{"--launchd-process-group=false"}, true},
		{"validation", []string{"--system-service", "--launchd-process-group", "--install-validation=id"}, true},
		{"empty-validation", []string{"--system-service", "--launchd-process-group", "--install-validation="}, true},
		{"unavailable-platform", []string{"--system-service", "--launchd-process-group"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			calls := 0
			run := func(context.Context) error { calls++; return nil }
			deps := Dependencies{RunDaemon: run, RunSystemServiceDaemon: run, RunInstallValidation: func(context.Context, string) error { calls++; return nil }}
			if tc.capability {
				deps.RunLaunchdServiceDaemon = run
			}
			code := Execute(context.Background(), append([]string{"daemon"}, tc.args...), &out, &out, deps)
			if code != 2 || calls != 0 {
				t.Fatalf("invalid launchd mode ran: calls=%d code=%d out=%s", calls, code, out.String())
			}
		})
	}
}

func TestDaemon_LaunchdCapabilityDoesNotRequireGeneralServiceCallback(t *testing.T) {
	for _, marked := range []bool{false, true} {
		var out bytes.Buffer
		calls := 0
		deps := Dependencies{RunLaunchdServiceDaemon: func(context.Context) error { calls++; return nil }}
		args := []string{"daemon", "--system-service"}
		wantCode, wantCalls := 2, 0
		if marked {
			args = append(args, "--launchd-process-group")
			wantCode, wantCalls = 0, 1
		}
		if code := Execute(context.Background(), args, &out, &out, deps); code != wantCode || calls != wantCalls {
			t.Fatalf("marked=%v code=%d calls=%d out=%s", marked, code, calls, out.String())
		}
	}
}

func TestDaemon_LaunchdProcessGroupIsHidden(t *testing.T) {
	command := newDaemonCommand(Dependencies{})
	flag := command.Flags().Lookup("launchd-process-group")
	if flag == nil || !flag.Hidden {
		t.Fatal("launchd marker is absent or publicly advertised")
	}
	var out bytes.Buffer
	if code := Execute(context.Background(), []string{"daemon", "--help"}, &out, &out, Dependencies{}); code != 0 || strings.Contains(out.String(), "launchd-process-group") {
		t.Fatalf("hidden mode leaked into help: code=%d out=%s", code, out.String())
	}
}
