package ui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"strings"
	"testing"
)

func TestExportOutcome_OriginalDetailsWithoutFileLogger(t *testing.T) {
	for _, kind := range []string{"failure", "panic", "warning", "cancel", "cancel_cleanup"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("token=fixture-export")
			runner := newExportRunner(ctx, func(_ context.Context, request logging.ExportRequest) (logging.ExportResult, error) {
				switch kind {
				case "panic":
					panic("token=fixture-export")
				case "warning":
					for i := 0; i < 70; i++ {
						request.OnWarning(cause)
					}
					return logging.ExportResult{Path: "fixture.zip"}, nil
				case "cancel":
					cancel()
					return logging.ExportResult{}, context.Canceled
				case "cancel_cleanup":
					cancel()
					return logging.ExportResult{}, errors.Join(context.Canceled, cause)
				default:
					return logging.ExportResult{}, cause
				}
			})
			results, ok := runner.Start(1, logging.ExportRequest{})
			if !ok {
				t.Fatal("export not started")
			}
			result := <-results
			runner.Wait()
			failures, ok := any(result).(interface{ DiagnosticErrors() []error })
			if !ok {
				t.Fatal("export errors have no shell contract")
			}
			if kind == "cancel" || kind == "warning" {
				if len(failures.DiagnosticErrors()) != 0 {
					t.Fatal("success/cancel became an error")
				}
			} else {
				errs := failures.DiagnosticErrors()
				if len(errs) != 1 || !strings.Contains(diagnostics.Capture(errs[0]).Text, "token=fixture-export") {
					t.Fatal("original export cause missing")
				}
			}
			if kind == "warning" {
				warnings, ok := any(result).(interface {
					Warnings() protocol.WarningOutcome
				})
				if !ok {
					t.Fatal("warning details have no shell contract")
				}
				outcome := warnings.Warnings()
				if len(outcome.Warnings) != 64 || outcome.WarningsOmitted != 6 {
					t.Fatalf("warnings=%d omitted=%d", len(outcome.Warnings), outcome.WarningsOmitted)
				}
				for _, warning := range outcome.Warnings {
					if warning.Diagnostic == nil || !strings.Contains(warning.Diagnostic.Detail, "token=fixture-export") {
						t.Fatal("warning original cause lost")
					}
				}
				if result.Err != nil || result.Result.Path != "fixture.zip" {
					t.Fatal("warning changed completed export")
				}
			}
		})
	}
}

func TestExportLocalFailures_ReachGlobalDetails(t *testing.T) {
	for _, kind := range []string{"clipboard", "time"} {
		t.Run(kind, func(t *testing.T) {
			cause := errors.New("clipboard token=fixture-copy")
			m := NewExportLogsModel(ExportLogsOptions{DefaultDir: t.TempDir(), WriteClipboard: func(string) error { return cause }})
			m.Open()
			var cmd tea.Cmd
			if kind == "clipboard" {
				m.resultPath = "fixture.zip"
				cmd, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			} else {
				m.rangeKind, m.from, m.to = logging.RangeBetween, "token=fixture-time", "invalid"
				cmd = m.submit()
			}
			if cmd == nil {
				t.Fatal("local failure was discarded")
			}
			msg, ok := cmd().(DiagnosticMsg)
			if !ok || msg.Err == nil {
				t.Fatal("missing global diagnostic")
			}
			want := "fixture-time"
			if kind == "clipboard" {
				want = "fixture-copy"
				if !errors.Is(msg.Err, cause) || m.resultPath != "fixture.zip" {
					t.Fatal("copy failure lost original error or archive path")
				}
			}
			if !strings.Contains(diagnostics.Capture(msg.Err).Text, want) {
				t.Fatal("raw cause missing")
			}
		})
	}
}

func TestExportPreviewFailure_PreservesCandidateAndWarning(t *testing.T) {
	cause := errors.New("preview token=fixture-path")
	m := NewExportLogsModel(ExportLogsOptions{DefaultDir: t.TempDir(), Exists: func(string, string) (bool, error) { return false, cause }})
	m.Open()
	original := m.output
	cmd, _ := m.Update(nil)
	if cmd == nil {
		t.Fatal("preview failure discarded")
	}
	outcome, ok := cmd().(interface {
		Warnings() protocol.WarningOutcome
	})
	if !ok || len(outcome.Warnings().Warnings) != 1 || !strings.Contains(outcome.Warnings().Warnings[0].Diagnostic.Detail, "fixture-path") {
		t.Fatal("preview warning missing")
	}
	if m.output != original || m.Pending() {
		t.Fatal("warning changed preview or started export")
	}
	if cmd, _ := m.Update(nil); cmd != nil {
		t.Fatal("preview warning replayed")
	}
}

func TestExportOutcome_OnlyOwnedCancellationUsesCancellationNotice(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		cancelled bool
	}{
		{"upstream deadline", context.DeadlineExceeded, false},
		{"cleanup", errors.Join(context.Canceled, errors.New("cleanup failed")), false},
		{"owned cancellation", context.Canceled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewExportLogsModel(ExportLogsOptions{DefaultDir: t.TempDir()})
			m.Open()
			m.pending = true
			m.generation = 1
			m.Update(exportResultMsg{Generation: 1, Err: tc.err, cancelled: tc.cancelled})
			if (m.message == ExportCancelled) != tc.cancelled || m.Pending() {
				t.Fatalf("wrong outcome notice: %s", m.message)
			}
		})
	}
}
