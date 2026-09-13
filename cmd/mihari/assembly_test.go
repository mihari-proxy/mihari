package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
)

func TestDaemonAssembly_BusinessFailureRetainsOwnedListener(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	runDaemon = func(_ context.Context, opts daemon.Options) error {
		calls++
		if opts.Listen == nil {
			t.Error("degraded control lost owned listener")
		}
		return nil
	}
	err = runDaemonWith(context.Background(), daemonRunDeps{Paths: paths, PrivateFS: fs, Listen: func(context.Context) (net.Listener, error) { return nil, errors.New("owned") }, LoadSettings: func(string, string) (config.Settings, bool, config.CommitResult, error) {
		return config.Settings{}, false, config.CommitResult{}, errors.New("invalid business settings")
	}})
	if err != nil || calls != 1 {
		t.Fatalf("business configuration failure did not retain degraded control: calls=%d err=%v", calls, err)
	}
}

func TestDaemonAssembly_MachineSnapshotUsesActualLogging(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	buildDaemonRuntime = func(_ platform.Paths, _ config.Settings, _ string, _ io.Writer, _ io.Writer, options app.RuntimeBuildOptions) (*app.RuntimeAssembly, error) {
		if options.DiagnosticReporter == nil {
			t.Error("degraded daemon build did not receive diagnostic reporter")
		}
		return nil, errors.New("invalid config")
	}
	runDaemon = func(_ context.Context, opts daemon.Options) error {
		if opts.SnapshotSource == nil {
			t.Error("system snapshot is not wired to actual logging owners")
		}
		if opts.Listen == nil {
			t.Error("degraded owned listener lost")
		}
		if opts.DiagnosticReporter == nil {
			t.Error("degraded control server did not receive diagnostic reporter")
		}
		return nil
	}
	if err := runDaemonWith(context.Background(), daemonRunDeps{Paths: paths, PrivateFS: fs, MachineSnapshot: true, Listen: func(context.Context) (net.Listener, error) { return nil, errors.New("owned") }}); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonAssembly_RuntimeBuildFailureIsRecordedBeforeDegradedStartup(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}

	var records bytes.Buffer
	openDaemonRuntime = func(context.Context, logging.RuntimeOptions) (daemonLoggingRuntime, error) {
		return daemonLoggingRuntime{
			Closer: io.NopCloser(strings.NewReader("")),
			Logger: slog.New(slog.NewJSONHandler(&records, &slog.HandlerOptions{Level: slog.LevelDebug})),
		}, nil
	}
	buildDaemonRuntime = func(platform.Paths, config.Settings, string, io.Writer, io.Writer, app.RuntimeBuildOptions) (*app.RuntimeAssembly, error) {
		return nil, &os.PathError{Op: "open", Path: "/private/runtime.yaml", Err: os.ErrPermission}
	}
	var degraded bool
	runDaemon = func(_ context.Context, options daemon.Options) error {
		degraded = options.Store.Load().Health == "degraded"
		if got := options.Store.Load().LastError; got != "daemon startup failed" {
			t.Fatalf("degraded LastError=%q", got)
		}
		return nil
	}

	if err := runDaemonWith(context.Background(), daemonRunDeps{
		Paths: paths, PrivateFS: fs, Version: "test",
		LoadSettings: func(string, string) (config.Settings, bool, config.CommitResult, error) {
			return config.Defaults(), false, config.CommitResult{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if !degraded {
		t.Fatal("runtime build failure did not continue through degraded daemon startup")
	}
	if !strings.Contains(records.String(), `"msg":"runtime_build_failed"`) || !strings.Contains(records.String(), "path operation open: permission denied") {
		t.Fatalf("runtime build diagnostic=%s", records.String())
	}
	if strings.Contains(records.String(), "/private/runtime.yaml") {
		t.Fatalf("runtime build diagnostic leaked a path: %s", records.String())
	}
}

func TestDaemonAssembly_PortConflictOnlyOpensRestrictedOnboarding(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	buildDaemonRuntime = func(platform.Paths, config.Settings, string, io.Writer, io.Writer, app.RuntimeBuildOptions) (*app.RuntimeAssembly, error) {
		return nil, &app.ManagedPortConflict{}
	}
	var recovered bool
	runDaemon = func(_ context.Context, options daemon.Options) error {
		recovered = options.Onboarding != nil && options.Runtime == nil && options.Store.Load().Health == "degraded"
		return nil
	}
	if err := runDaemonWith(context.Background(), daemonRunDeps{Paths: paths, PrivateFS: fs, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("confirmed port conflict did not expose restricted onboarding")
	}
}

func TestDaemonAssembly_ServiceDiagnosticStderrRequiresExplicitNonJSONServiceMode(t *testing.T) {
	writer := &bytes.Buffer{}
	for _, args := range [][]string{
		{"daemon", "--system-service"},
		{"daemon", "--system-service=1"},
		{"daemon", "--system-service=t"},
		{"daemon", "--system-service=T"},
		{"daemon", "--system-service=TRUE"},
		{"daemon", "--system-service=True"},
		{"daemon", "--system-service=false", "--system-service=1"},
	} {
		if got := daemonServiceDiagnosticStderr(args, writer); got != writer {
			t.Fatalf("args=%q service diagnostic stderr=%v want injected writer", args, got)
		}
	}
	for _, args := range [][]string{
		{"daemon"},
		{"daemon", "--system-service=false"},
		{"daemon", "--system-service=0"},
		{"daemon", "--system-service=f"},
		{"daemon", "--system-service=F"},
		{"daemon", "--system-service=FALSE"},
		{"daemon", "--system-service=False"},
		{"daemon", "--system-service", "--system-service=false"},
		{"daemon", "--", "--system-service"},
		{"daemon", "--system-service", "--json"},
		{"daemon", "--system-service", "--json=1"},
	} {
		if got := daemonServiceDiagnosticStderr(args, writer); got != nil {
			t.Fatalf("args=%q diagnostic stderr=%v want nil", args, got)
		}
	}
}

func TestDaemonAssembly_LoggingFailureStderrKeepsTextDaemonOutputAndExcludesJSON(t *testing.T) {
	writer := &bytes.Buffer{}
	for _, args := range [][]string{
		{"daemon"},
		{"daemon", "--system-service"},
		{"daemon", "--install-validation=transaction"},
	} {
		if got := daemonLoggingFailureStderr(args, writer); got != writer {
			t.Fatalf("args=%q logging failure stderr=%v want injected writer", args, got)
		}
	}
	for _, args := range [][]string{
		{"status"},
		{"daemon", "--json"},
		{"daemon", "--json=1"},
		{"daemon", "--json=t"},
		{"daemon", "--json=T"},
		{"daemon", "--json=TRUE"},
		{"daemon", "--json=True"},
		{"daemon", "--json=false", "--json=1"},
	} {
		if got := daemonLoggingFailureStderr(args, writer); got != nil {
			t.Fatalf("args=%q logging failure stderr=%v want nil", args, got)
		}
	}
	for _, args := range [][]string{
		{"daemon", "--json=0"},
		{"daemon", "--json=f"},
		{"daemon", "--json=F"},
		{"daemon", "--json=FALSE"},
		{"daemon", "--json=False"},
		{"daemon", "--json", "--json=false"},
		{"--json=0", "daemon"},
		{"daemon", "--", "--json"},
	} {
		if got := daemonLoggingFailureStderr(args, writer); got != writer {
			t.Fatalf("args=%q logging failure stderr=%v want injected writer", args, got)
		}
	}
}

func TestDaemonAssembly_OutputSelectorsMatchActualBooleanRendering(t *testing.T) {
	for _, spelling := range []string{"1", "t", "T", "TRUE", "true", "True"} {
		t.Run("json="+spelling, func(t *testing.T) {
			args := []string{"daemon", "--json=" + spelling}
			var output bytes.Buffer
			if outlet := daemonLoggingFailureStderr(args, &output); outlet != nil {
				_, _ = outlet.Write([]byte("early logging fallback\n"))
			}
			code := cli.Execute(context.Background(), args, &output, &output, cli.Dependencies{RunDaemon: func(context.Context) error {
				return protocol.APIError{Code: protocol.CodeInvalidState, Message: "daemon unavailable"}
			}})
			var envelope protocol.ErrorEnvelope
			if code != cli.ExitInvalidState || json.Unmarshal(output.Bytes(), &envelope) != nil || envelope.Error.Code != protocol.CodeInvalidState {
				t.Fatalf("code=%d output=%q", code, output.String())
			}
		})
	}

	args := []string{"--json=0", "daemon"}
	var output bytes.Buffer
	if outlet := daemonLoggingFailureStderr(args, &output); outlet != nil {
		_, _ = outlet.Write([]byte("early logging fallback\n"))
	}
	code := cli.Execute(context.Background(), args, &output, &output, cli.Dependencies{RunDaemon: func(context.Context) error {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "daemon unavailable"}
	}})
	if code != cli.ExitInvalidState || output.String() != "early logging fallback\nError: daemon unavailable\n" {
		t.Fatalf("code=%d output=%q", code, output.String())
	}
}
