package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestDaemonAssembly_DiagnosticHistorySharesRuntimeOccurrences(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	buildDaemonRuntime = func(_ platform.Paths, _ config.Settings, _ string, _, _ io.Writer, options app.RuntimeBuildOptions) (*app.RuntimeAssembly, error) {
		options.DiagnosticReporter(context.Background(), diagnostics.Record{Level: slog.LevelError, Event: "fixture.failed", Err: errors.New("token=fixture-runtime")})
		return &app.RuntimeAssembly{}, nil
	}
	runDaemon = func(_ context.Context, options daemon.Options) error {
		if options.DiagnosticHistory == nil {
			t.Fatal("runtime history was not passed to daemon")
		}
		page := options.DiagnosticHistory.List("", 0, 100)
		if len(page.Records) != 1 {
			t.Fatalf("history=%+v", page)
		}
		detail := options.DiagnosticHistory.Get(page.Records[0].ID)
		if detail.Diagnostic == nil || detail.Diagnostic.Detail != "token=fixture-runtime" {
			t.Fatalf("runtime occurrence lost: %+v", detail)
		}
		return nil
	}
	if err := runDaemonWith(context.Background(), daemonRunDeps{Paths: paths, PrivateFS: fs}); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonAssembly_EarlySettingsFailureRemainsInDegradedHistory(t *testing.T) {
	resetDaemonRunSeamsForTest(t)
	paths := absoluteTempPaths(t)
	fs, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	runDaemon = func(_ context.Context, options daemon.Options) error {
		if options.DiagnosticHistory == nil {
			t.Fatal("early failure has no history")
		}
		page := options.DiagnosticHistory.List("", 0, 100)
		if len(page.Records) != 1 {
			t.Fatalf("history=%+v", page)
		}
		detail := options.DiagnosticHistory.Get(page.Records[0].ID)
		if detail.Diagnostic == nil || !strings.Contains(detail.Diagnostic.Detail, "password=fixture-settings") {
			t.Fatalf("early cause lost: %+v", detail)
		}
		return nil
	}
	if err := runDaemonWith(context.Background(), daemonRunDeps{
		Paths: paths, PrivateFS: fs,
		Listen: func(context.Context) (net.Listener, error) { return nil, errors.New("fixture should not listen") },
		LoadSettings: func(string, string) (config.Settings, bool, config.CommitResult, error) {
			return config.Settings{}, false, config.CommitResult{}, errors.New("password=fixture-settings")
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonStartupFallback_PreservesOriginalCause(t *testing.T) {
	var output bytes.Buffer
	cause := &os.PathError{Op: "open", Path: "/private/fixture settings/token=original", Err: os.ErrPermission}
	reportDaemonStartupFailure(&output, "load settings", cause)
	if !strings.Contains(output.String(), cause.Error()) {
		t.Fatal("startup fallback hid original cause")
	}
}
