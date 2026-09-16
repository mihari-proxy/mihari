package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestBuildExportLogs_DialogPreservesOutcomeAndOriginalWarning(t *testing.T) {
	for _, outcome := range []string{"success", "cancel", "upstream_cancel", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			paths := absoluteTempPaths(t)
			fs, err := platform.NewPrivateFS(paths.Root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = fs.Close() })
			if err := fs.EnsureDir(paths.LogDir); err != nil {
				t.Fatal(err)
			}
			f, err := fs.OpenAppend(paths.DaemonLog)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := io.WriteString(f, `{"time":"2026-09-05T00:00:00Z","msg":"ok"}`+"\n")
			if err := errors.Join(writeErr, f.Close()); err != nil {
				t.Fatal(err)
			}
			original := exportLogsFn
			defer func() { exportLogsFn = original }()
			var path string
			exportLogsFn = func(ctx context.Context, req logging.ExportRequest) (logging.ExportResult, error) {
				result := logging.ExportResult{}
				var err error
				switch outcome {
				case "success":
					result, err = original(ctx, req)
					path = result.Path
				case "cancel":
					cancel()
					err = ctx.Err()
				case "upstream_cancel":
					err = context.Canceled
				default:
					err = errors.New("private-failure-secret")
				}
				if req.OnWarning != nil {
					req.OnWarning(errors.New("/private/path token=warning-secret"))
				}
				return result, err
			}
			options := buildExportLogs(paths)(tui.NewLoggingResources(nil, logging.NewRedactor(), fs))
			options.Context = ctx
			options.Now = func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) }
			dialog := ui.NewExportLogsModel(options)
			t.Cleanup(dialog.CancelAndWait)
			dialog.Open()
			dialog.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			cmd, _ := dialog.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			message := cmd()
			dialog.Update(message)
			warnings, ok := message.(interface {
				Warnings() protocol.WarningOutcome
			})
			if !ok {
				t.Fatal("export result lost warning contract")
			}
			got := warnings.Warnings()
			if len(got.Warnings) != 1 || got.Warnings[0].Diagnostic == nil || !strings.Contains(got.Warnings[0].Diagnostic.Detail, "/private/path token=warning-secret") {
				t.Fatal("export warning original cause lost")
			}
			failures, ok := message.(interface{ DiagnosticErrors() []error })
			if !ok {
				t.Fatal("export result lost failure contract")
			}
			if outcome == "failure" || outcome == "upstream_cancel" {
				errs := failures.DiagnosticErrors()
				want := "private-failure-secret"
				if outcome == "upstream_cancel" {
					want = context.Canceled.Error()
				}
				if len(errs) != 1 || !strings.Contains(diagnostics.Capture(errs[0]).Text, want) {
					t.Fatal("actual export failure lost")
				}
			} else if len(failures.DiagnosticErrors()) != 0 {
				t.Fatal("success or active cancellation became a failure")
			}
			view := dialog.View(200, 40)
			compact := strings.Join(strings.Fields(ansi.Strip(view)), "")
			compact = strings.NewReplacer("│", "", "\r", "", "\n", "").Replace(compact)
			if !strings.Contains(view, "Temporary export data may remain") {
				t.Errorf("warning not visible: %s", view)
			}
			want := map[string]string{"success": ui.ExportComplete, "cancel": ui.ExportCancelled, "failure": ui.ExportFailed, "upstream_cancel": ui.ExportFailed}[outcome]
			if !strings.Contains(view, want) {
				t.Errorf("primary outcome lost: %s", view)
			}
			if outcome == "success" && (path == "" || !strings.Contains(compact, filepath.Base(path))) {
				t.Error("published path lost")
			}
		})
	}
}
