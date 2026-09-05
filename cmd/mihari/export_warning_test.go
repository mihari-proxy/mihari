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
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/tui"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestBuildExportLogs_DialogPreservesOutcomeAndSanitizedWarning(t *testing.T) {
	for _, outcome := range []string{"success", "cancel", "failure"} {
		t.Run(outcome, func(t *testing.T) {
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
			options.Now = func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) }
			dialog := ui.NewExportLogsModel(options)
			t.Cleanup(dialog.CancelAndWait)
			dialog.Open()
			dialog.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			cmd, _ := dialog.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			dialog.Update(cmd())
			view := dialog.View(200, 40)
			compact := strings.Join(strings.Fields(ansi.Strip(view)), "")
			compact = strings.NewReplacer("│", "", "\r", "", "\n", "").Replace(compact)
			if !strings.Contains(view, "Temporary export data may remain") {
				t.Errorf("warning not visible: %s", view)
			}
			for _, secret := range []string{"warning-secret", "private-failure-secret", "/private/path"} {
				if strings.Contains(compact, secret) {
					t.Error("raw warning/error leaked")
				}
			}
			want := map[string]string{"success": ui.ExportComplete, "cancel": ui.ExportCancelled, "failure": ui.ExportFailed}[outcome]
			if !strings.Contains(view, want) {
				t.Errorf("primary outcome lost: %s", view)
			}
			if outcome == "success" && (path == "" || !strings.Contains(compact, filepath.Base(path))) {
				t.Error("published path lost")
			}
		})
	}
}
