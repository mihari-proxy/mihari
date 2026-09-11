package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestRunDaemonWith_CloseFailureUsesIndependentSafeOutlet(t *testing.T) {
	for _, test := range []struct{ name, fail, output, openError string }{
		{"stdout", "stdout", "enabled", ""},
		{"stderr", "stderr", "enabled", ""},
		{"mihomo", "mihomo", "enabled", ""},
		{"daemon", "daemon", "enabled", ""},
		{"private fs", "fs", "enabled", ""},
		{"partial logging", "daemon", "enabled", "mihomo"},
		{"early settings", "fs", "enabled", "settings"},
		{"nil outlet", "daemon", "nil", ""},
		{"failed outlet", "daemon", "failed", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetDaemonRunSeamsForTest(t)
			paths := absoluteTempPaths(t)
			fs, err := platform.NewPrivateFS(paths.Root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = fs.Close() })
			var order []string
			failure := errors.New("close " + test.fail + ": shutdown-secret /private/secret-data/logs\r\nfailed")
			runFailure := errors.New("run failed")
			openFailure := errors.New("open failed")
			closers := map[string]*countingCloser{}
			for _, name := range []string{"stdout", "stderr", "mihomo", "daemon", "fs"} {
				closers[name] = &countingCloser{name: name, order: &order}
			}
			closers[test.fail].err = failure
			newDaemonResources = func(io.Closer) *daemonLoggingResources { return &daemonLoggingResources{PrivateFS: closers["fs"]} }
			openDaemonRuntime = func(_ context.Context, options logging.RuntimeOptions) (daemonLoggingRuntime, error) {
				if options.Component == test.openError {
					return daemonLoggingRuntime{}, openFailure
				}
				return daemonLoggingRuntime{Closer: closers[options.Component], Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, nil
			}
			newDaemonCapture = func(_ *slog.Logger, _ slog.Level, stream string) logging.LineCaptureWriter {
				return &countingCapture{closers[stream]}
			}
			buildDaemonRuntime = func(platform.Paths, config.Settings, string, io.Writer, io.Writer, app.RuntimeBuildOptions) (*app.RuntimeAssembly, error) {
				return &app.RuntimeAssembly{}, nil
			}
			runDaemon = func(context.Context, daemon.Options) error { order = append(order, "run"); return runFailure }
			var output, startup bytes.Buffer
			failedOutput := &failingDiagnosticWriter{}
			var outlet io.Writer
			switch test.output {
			case "enabled":
				outlet = shutdownOutputWriter(func(p []byte) (int, error) {
					if closers["fs"].calls != 1 {
						t.Fatal("fallback ran before all captures, runtimes and fs closed")
					}
					return output.Write(p)
				})
			case "failed":
				outlet = failedOutput
			}
			deps := daemonRunDeps{Paths: paths, PrivateFS: fs, Token: "shutdown-secret", Version: "test", LoggingFailureStderr: outlet, DiagnosticStderr: &startup}
			if test.openError == "settings" {
				deps.LoadSettings = func(string, string) (config.Settings, bool, config.CommitResult, error) {
					return config.Settings{}, false, config.CommitResult{}, openFailure
				}
			}
			err = runDaemonWith(context.Background(), deps)
			if !errors.Is(err, failure) {
				t.Fatal("close error was lost")
			}
			wantOrder := []string{"run", "stdout", "stderr", "mihomo", "daemon", "fs"}
			wantCause := runFailure
			if test.openError != "" {
				wantOrder = []string{"daemon", "fs"}
				wantCause = openFailure
			}
			if test.openError == "settings" {
				wantOrder = []string{"fs"}
				var apiErr protocol.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != protocol.CodeDataFailure || apiErr.Message != "load settings" {
					t.Fatal("early public error changed")
				}
			} else if !errors.Is(err, wantCause) {
				t.Fatal("original run/open error was lost")
			}
			if !slices.Equal(order, wantOrder) {
				t.Fatalf("order=%q want=%q", order, wantOrder)
			}
			for name, c := range closers {
				want := 0
				if slices.Contains(wantOrder, name) {
					want = 1
				}
				if c.calls != want {
					t.Fatalf("%s closed %d times want %d", name, c.calls, want)
				}
			}
			if test.output == "enabled" {
				got := output.String()
				if strings.Count(got, "logging: cleanup:") != 1 || strings.Count(got, "\n") != 1 {
					t.Fatalf("cleanup report count=%d lines=%d want one", strings.Count(got, "logging: cleanup:"), strings.Count(got, "\n"))
				}
				if test.openError == "settings" && got != "logging: cleanup: close logging resources failed\n" {
					t.Fatal("early close exposed unregistered cause")
				}
				for _, raw := range []string{"shutdown-secret", "/private/", "secret-data", "\r"} {
					if strings.Contains(got, raw) {
						t.Fatal("cleanup report leaked secret, path or CR")
					}
				}
			} else if output.Len() != 0 {
				t.Fatal("disabled outlet emitted output")
			}
			if test.output == "failed" && failedOutput.writes != 1 {
				t.Fatalf("fallback attempts=%d want 1", failedOutput.writes)
			}
			if test.openError == "" && startup.Len() != 0 {
				t.Fatal("close failure used startup diagnostic outlet")
			}
		})
	}
}

type shutdownOutputWriter func([]byte) (int, error)

func (w shutdownOutputWriter) Write(p []byte) (int, error) { return w(p) }
