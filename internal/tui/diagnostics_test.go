package tui

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func diagnosticKey(code rune) tea.KeyPressMsg { return tea.KeyPressMsg(tea.Key{Code: code}) }

func TestDiagnosticsF2_PreservesInputAndUnderlyingConfirmation(t *testing.T) {
	for _, page := range []ui.PageID{ui.PageSetup, ui.PageSystem} {
		model := NewModel()
		model.active = page
		model.inputMode = ui.InputText
		model.focus = ui.Focus{Area: ui.FocusContent, Page: page}
		confirmation := NewConfirmation("Pending fixture confirmation", "keep form", "keep selection", "")
		confirmation.selected = 0
		model.modal = confirmation
		next, _ := model.Update(diagnosticKey(tea.KeyF2))
		opened := next.(Model)
		if !strings.Contains(opened.View().Content, "Diagnostics") || !strings.Contains(opened.View().Content, "No diagnostic records") {
			t.Fatalf("F2 did not open from %s", page)
		}
		next, _ = opened.Update(diagnosticKey(tea.KeyEscape))
		restored := next.(Model)
		if restored.modal != confirmation || restored.modal.selected != 0 || restored.inputMode != ui.InputText || restored.focus != model.focus {
			t.Fatal("F2 discarded or confirmed the previous UI state")
		}
	}
}

func TestDiagnosticsF2_SeesLocalOwnerWithoutFileLogger(t *testing.T) {
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "local", MaxRecords: 128, MaxBytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	owner := diagnostics.NewOwner(history, nil)
	failure := diagnostics.ReportError(context.Background(), owner.Report, diagnostics.Record{Component: "tui", Event: "local.failed", Level: slog.LevelError, Err: errors.New("token=fixture-local-owner")})
	model := NewModel()
	model.localDiagnosticHistory = history
	next, _ := model.Update(diagnosticKey(tea.KeyF2))
	model = next.(Model)
	if !strings.Contains(model.View().Content, "token=fixture-local-owner") {
		t.Fatal("F2 depends on a file logger")
	}
	next, _ = model.Update(ui.DiagnosticMsg{Err: failure})
	model = next.(Model)
	if len(model.diagnosticWindow.entries) != 1 {
		t.Fatal("the same owner occurrence was inserted twice")
	}
}

func TestDiagnosticsF2_PrefersCurrentPageAndScrollsOriginalDetail(t *testing.T) {
	model := NewModel()
	model.active = ui.PageSetup
	current := protocol.Diagnostic{ID: "daemon:1", State: protocol.DiagnosticAvailable, Component: "setup", Summary: "fixture setup failed", Detail: strings.Repeat("original line\n", 100) + "token=fixture-last\x1b[31m", Time: time.Unix(1, 0)}
	newer := protocol.Diagnostic{ID: "daemon:2", State: protocol.DiagnosticAvailable, Component: "rules", Summary: "fixture rules failed", Detail: "rules detail", Time: time.Unix(2, 0)}
	next, _ := model.Update(ui.DiagnosticMsg{Page: ui.PageSetup, Diagnostic: &current})
	model = next.(Model)
	next, _ = model.Update(ui.DiagnosticMsg{Page: ui.PageRules, Diagnostic: &newer})
	model = next.(Model)
	if strings.Contains(model.View().Content, "Diagnostics") {
		t.Fatal("background occurrence opened the window")
	}
	next, _ = model.Update(diagnosticKey(tea.KeyF2))
	model = next.(Model)
	if !strings.Contains(model.View().Content, "fixture setup failed") || !strings.Contains(model.View().Content, "original line") {
		t.Fatal("current page diagnostic was not selected")
	}
	next, _ = model.Update(diagnosticKey(tea.KeyTab))
	model = next.(Model)
	next, _ = model.Update(diagnosticKey(tea.KeyEnd))
	model = next.(Model)
	if !strings.Contains(model.View().Content, `token=fixture-last\x1b[31m`) || strings.Contains(model.View().Content, "\x1b[31m") {
		t.Fatal("detail does not scroll safely to the unredacted end")
	}
}

type diagnosticActionResult struct{ failure error }

func (r diagnosticActionResult) Err() error { return r.failure }

func TestDiagnosticsF2_RecordsActionFailureWithoutOpening(t *testing.T) {
	model := NewModel()
	model.active = ui.PageRules
	next, _ := model.Update(actionCompletedMsg{Intent: ui.ActionIntentMsg{Page: ui.PageRules, Key: "rules:refresh", Title: "Refresh rules"}, Result: diagnosticActionResult{failure: errors.New("provider token=fixture-action")}})
	model = next.(Model)
	if model.diagnosticWindow.open {
		t.Fatal("failure stole focus")
	}
	next, _ = model.Update(diagnosticKey(tea.KeyF2))
	model = next.(Model)
	if !strings.Contains(model.View().Content, "token=fixture-action") {
		t.Fatal("action failure never reached global details")
	}
}

func TestDiagnosticsF2_CopyAndEvictionKeepPinnedOccurrence(t *testing.T) {
	model := NewModel()
	model.diagnosticWindow.maxRecords = 1
	original := "password=fixture\x1b]52;fixture\a\n" + strings.Repeat("line\n", 40)
	first := protocol.Diagnostic{ID: "daemon:1", State: protocol.DiagnosticAvailable, Summary: "first", Detail: original, Time: time.Unix(1, 0)}
	next, _ := model.Update(ui.DiagnosticMsg{Diagnostic: &first})
	model = next.(Model)
	next, _ = model.Update(diagnosticKey(tea.KeyF2))
	model = next.(Model)
	next, _ = model.Update(diagnosticKey(tea.KeyTab))
	model = next.(Model)
	next, _ = model.Update(diagnosticKey(tea.KeyEnd))
	model = next.(Model)
	scroll := model.diagnosticWindow.scroll
	second := protocol.Diagnostic{ID: "daemon:2", State: protocol.DiagnosticAvailable, Summary: "second", Detail: original, Time: time.Unix(2, 0)}
	next, _ = model.Update(ui.DiagnosticMsg{Diagnostic: &second})
	model = next.(Model)
	if len(model.diagnosticWindow.entries) != 1 || model.diagnosticWindow.selected != first.ID || model.diagnosticWindow.scroll != scroll {
		t.Fatal("background insertion moved the selection or scroll")
	}
	var copied string
	model.diagnosticWindow.copyText = func(value string) error { copied = value; return errors.New("clipboard fixture error") }
	next, cmd := model.Update(diagnosticKey('c'))
	model = next.(Model)
	if cmd == nil {
		t.Fatal("copy was not offered for the pinned record")
	}
	next, _ = model.Update(cmd())
	model = next.(Model)
	if copied != original || !model.diagnosticWindow.open || model.diagnosticWindow.selected != first.ID || model.diagnosticWindow.scroll != scroll || model.diagnosticWindow.copyStatus != "Copy failed" {
		t.Fatal("copy escaped the original text or discarded the window")
	}
	next, _ = model.Update(diagnosticKey(tea.KeyEscape))
	model = next.(Model)
	if model.diagnosticWindow.pinned.Detail != "" {
		t.Fatal("closing retained the pinned body")
	}
}

func TestDiagnostics_ResourceFailureRetainsSnapshotAndShowsSummary(t *testing.T) {
	model := NewModel()
	model.active = ui.PageSystem
	model.core = protocol.CoreStatus{Status: "running"}
	next, _ := model.Update(sessionEventMsg{Open: true, Event: session.Event{Kind: session.EventCore, Err: errors.New("fixture core read failed")}})
	model = next.(Model)
	if model.core.Status != "running" || !strings.Contains(model.footerGlobalSegment(), "fixture core read failed") {
		t.Fatal("resource failure discarded previous data or failed to display its summary")
	}
	next, _ = model.Update(sessionEventMsg{Open: true, Event: session.Event{Kind: session.EventCore, Core: protocol.CoreStatus{Status: "running"}}})
	model = next.(Model)
	if strings.Contains(model.footerGlobalSegment(), "fixture core read failed") || len(model.diagnosticWindow.entries) != 1 {
		t.Fatal("recovery must clear current failure indicator and retain occurrence history")
	}
}

type detailFixtureClient struct {
	result protocol.DiagnosticResult
	err    error
	calls  int
}

func (c *detailFixtureClient) Diagnostic(context.Context, string) (protocol.DiagnosticResult, error) {
	c.calls++
	return c.result, c.err
}

func TestDiagnosticsF2_LateDetailCannotReplaceAnotherSelection(t *testing.T) {
	model := NewModel()
	client := &detailFixtureClient{result: protocol.DiagnosticResult{State: protocol.DiagnosticAvailable, Diagnostic: &protocol.Diagnostic{ID: "remote:1", State: protocol.DiagnosticAvailable, Detail: "fixture first original"}}}
	model.diagnosticWindow.client = client
	first := protocol.Diagnostic{ID: "remote:1", State: protocol.DiagnosticReference, Summary: "fixture first", Time: time.Unix(2, 0)}
	second := protocol.Diagnostic{ID: "remote:2", State: protocol.DiagnosticAvailable, Summary: "fixture second", Detail: "fixture second original", Time: time.Unix(1, 0)}
	model.recordDiagnostic(nil, &first, ui.PageOverview)
	model.recordDiagnostic(nil, &second, ui.PageOverview)
	next, fetch := model.Update(diagnosticKey(tea.KeyF2))
	model = next.(Model)
	if fetch == nil {
		t.Fatal("reference not queried")
	}
	late := fetch()
	next, _ = model.Update(diagnosticKey(tea.KeyDown))
	model = next.(Model)
	next, _ = model.Update(late)
	model = next.(Model)
	if model.diagnosticWindow.pinned.Detail != "fixture second original" || client.calls != 1 {
		t.Fatal("late query replaced selected occurrence")
	}
}

func TestDiagnosticsF2_ListShowsSeveritySourceAndSummary(t *testing.T) {
	model := NewModel()
	model.width, model.height = 50, 25
	snapshot := protocol.Diagnostic{ID: "fixture:1", State: protocol.DiagnosticAvailable, Severity: "warning", Component: "runtime", Summary: "fixture failure summary", Detail: "original body"}
	model.recordDiagnostic(nil, &snapshot, ui.PageOverview)
	next, _ := model.Update(diagnosticKey(tea.KeyF2))
	model = next.(Model)
	view := model.View().Content
	for _, want := range []string{"warning", "runtime", "fixture failure summary"} {
		if !strings.Contains(view, want) {
			t.Fatalf("list metadata missing %s", want)
		}
	}
}

type diagnosticBatchFixture struct {
	failures []error
	warnings protocol.WarningOutcome
}

func (r diagnosticBatchFixture) Err() error                        { return errors.Join(r.failures...) }
func (r diagnosticBatchFixture) DiagnosticErrors() []error         { return r.failures }
func (r diagnosticBatchFixture) Warnings() protocol.WarningOutcome { return r.warnings }
func TestDiagnostics_ResultRetainsIndependentFailuresAndWarnings(t *testing.T) {
	model := NewModel()
	first := protocol.Diagnostic{ID: "daemon:1", State: protocol.DiagnosticAvailable, Detail: "fixture first cause", Summary: "first"}
	second := protocol.Diagnostic{ID: "daemon:2", State: protocol.DiagnosticAvailable, Detail: "fixture second cause", Summary: "second"}
	warning := protocol.Diagnostic{ID: "daemon:3", State: protocol.DiagnosticAvailable, Severity: "warning", Detail: "token=fixture-warning", Summary: "committed with warning"}
	result := diagnosticBatchFixture{failures: []error{diagnostics.WithSnapshot(errors.New("first"), first), diagnostics.WithSnapshot(errors.New("second"), second)}, warnings: protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: warning.Summary, Diagnostic: &warning}}}}
	next, _ := model.Update(ui.PageResultMsg{Page: ui.PageSetup, Result: result})
	model = next.(Model)
	if len(model.diagnosticWindow.entries) != 3 {
		t.Fatalf("independent outcomes lost: %d", len(model.diagnosticWindow.entries))
	}
	if model.diagnosticWindow.open {
		t.Fatal("background outcomes stole focus")
	}
	for _, entry := range model.diagnosticWindow.entries {
		if entry.page != ui.PageSetup || entry.snapshot.Detail == "" {
			t.Fatal("outcome lost page or raw detail")
		}
	}
}
func TestDiagnostics_SessionWarningIsVisibleWithoutChangingSuccess(t *testing.T) {
	model := NewModel()
	model.active = ui.PageSubscriptions
	warning := protocol.Diagnostic{ID: "daemon:warning", State: protocol.DiagnosticAvailable, Severity: "warning", Summary: "saved but synchronization failed", Detail: "fixture warning original"}
	status := protocol.SubscriptionList{Schema: "mihari/v1", Revision: 42, WarningOutcome: protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: warning.Summary, Diagnostic: &warning}}}}
	next, _ := model.Update(sessionEventMsg{Open: true, Event: session.Event{Kind: session.EventSubscriptions, Subscriptions: status}})
	model = next.(Model)
	if model.subscriptions.Revision != 42 || len(model.diagnosticWindow.entries) != 1 || !strings.Contains(model.footerGlobalSegment(), warning.Summary) {
		t.Fatal("successful warning disappeared or changed committed result")
	}
}

func TestDiagnostics_LoggingWarningKeepsOriginAfterPageSwitch(t *testing.T) {
	model := NewModel()
	model.active = ui.PageOverview
	warning := protocol.Diagnostic{ID: "fixture:logging-warning", State: protocol.DiagnosticAvailable, Summary: "logging saved", Detail: "token=fixture-sync", Severity: "warning"}
	result := ui.LoggingObservedMsg{Status: protocol.LoggingStatus{WarningOutcome: protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: warning.Summary, Diagnostic: &warning}}}}}
	next, _ := model.Update(result)
	model = next.(Model)
	entries := model.diagnosticWindow.entries
	if len(entries) != 1 || entries[0].page != ui.PageSystem || entries[0].snapshot.Detail != warning.Detail {
		t.Fatal("committed logging warning lost or assigned to active page")
	}
	if model.diagnosticWindow.open {
		t.Fatal("warning took focus")
	}
	model.active = ui.PageSystem
	next, _ = model.Update(diagnosticKey(tea.KeyF2))
	if next.(Model).diagnosticWindow.pinned.Detail != warning.Detail {
		t.Fatal("F2 lost logging warning")
	}
}

func TestDiagnosticsF2_ExportResultsReachDetailsThroughActiveModal(t *testing.T) {
	for _, warning := range []bool{false, true} {
		model := NewModel()
		model.width, model.height = 100, 30
		model.active = ui.PageSystem
		model.exportLogs = ui.NewExportLogsModel(ui.ExportLogsOptions{DefaultDir: t.TempDir(), Export: func(_ context.Context, request logging.ExportRequest) (logging.ExportResult, error) {
			cause := errors.New("export token=fixture-modal\noriginal detail")
			if warning {
				request.OnWarning(cause)
				return logging.ExportResult{Path: "fixture.zip"}, nil
			}
			return logging.ExportResult{}, cause
		}})
		model.exportLogs.Open()
		for i := 0; i < 2; i++ {
			next, _ := model.Update(diagnosticKey(tea.KeyTab))
			model = next.(Model)
		}
		next, cmd := model.Update(diagnosticKey(tea.KeyEnter))
		model = next.(Model)
		if cmd == nil {
			t.Fatal("export did not start")
		}
		next, _ = model.Update(cmd())
		model = next.(Model)
		if len(model.diagnosticWindow.entries) != 1 || model.diagnosticWindow.entries[0].page != ui.PageLogs {
			t.Fatal("actual export result never reached global history")
		}
		next, _ = model.Update(diagnosticKey(tea.KeyF2))
		model = next.(Model)
		if !model.diagnosticWindow.open || !strings.Contains(model.diagnosticWindow.pinned.Detail, "token=fixture-modal") {
			t.Fatal("F2 cannot inspect original export details over modal")
		}
		next, _ = model.Update(diagnosticKey(tea.KeyEscape))
		model = next.(Model)
		if model.exportLogs.Closed() || model.diagnosticWindow.open {
			t.Fatal("closing diagnostic discarded export dialog")
		}
	}
}

func TestDiagnostics_LocalInstallationAndCleanupKeepOrigin(t *testing.T) {
	cause := errors.New("local task token=fixture-installation")
	for _, message := range []tea.Msg{installationStatusMsg{err: cause}, installationPlanMsg{err: cause}, discardPreparedResultMsg{err: cause}} {
		model := NewModel()
		model.active = ui.PageOverview
		next, _ := model.Update(message)
		model = next.(Model)
		if len(model.diagnosticWindow.entries) != 1 {
			t.Fatalf("local result %T lost from history", message)
		}
		entry := model.diagnosticWindow.entries[0]
		if entry.page != ui.PageSystem || !strings.Contains(entry.snapshot.Detail, "token=fixture-installation") {
			t.Fatalf("local result %T lost origin/cause", message)
		}
	}
}

func TestDiagnostics_InstallationPlanCancellationKeepsCleanupCause(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		model := NewModel()
		model.setInstallationActions(InstallationActions{
			Elevated: func() bool { return true },
			Plan: func(ctx context.Context, _ app.InstallationPlanRequest) (app.InstallationPlan, error) {
				cause := ctx.Err()
				if cleanup {
					cause = errors.Join(cause, errors.New("cleanup token=fixture-plan"))
				}
				return app.InstallationPlan{}, cause
			},
			Execute: func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
				t.Fatal("cancelled preview must not execute")
				return app.InstallationOutcome{}, nil
			},
		})
		model.installation.visible = true
		next, cmd := model.Update(diagnosticKey(tea.KeyEnter))
		model = next.(Model)
		if cmd == nil {
			t.Fatal("plan did not start")
		}
		next, _ = model.Update(diagnosticKey(tea.KeyEscape))
		model = next.(Model)
		next, _ = model.Update(cmd())
		model = next.(Model)
		entries := model.diagnosticWindow.entries
		if !cleanup && len(entries) != 0 {
			t.Fatal("cancelled preview became a failure")
		}
		if cleanup && (len(entries) != 1 || !strings.Contains(entries[0].snapshot.Detail, "token=fixture-plan")) {
			t.Fatal("preview cleanup failure lost")
		}
	}
}

func TestDiagnostics_InstallationValidationFailureIsInspectable(t *testing.T) {
	for _, kind := range []string{"status", "plan"} {
		t.Run(kind, func(t *testing.T) {
			model := NewModel()
			model.setInstallationActions(InstallationActions{
				Inspect: func(context.Context) (protocol.InstallationStatus, error) {
					return protocol.InstallationStatus{Schema: "fixture-invalid"}, nil
				},
				Elevated: func() bool { return true },
				Plan: func(context.Context, app.InstallationPlanRequest) (app.InstallationPlan, error) {
					return app.InstallationPlan{Mode: app.InstallationModeRepair}, nil
				},
				Execute: func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
					t.Fatal("invalid plan executed")
					return app.InstallationOutcome{}, nil
				},
			})
			var cmd tea.Cmd
			if kind == "status" {
				cmd = model.inspectInstallation()
			} else {
				model.installation.visible = true
				next, start := model.Update(diagnosticKey(tea.KeyEnter))
				model = next.(Model)
				cmd = start
			}
			next, _ := model.Update(cmd())
			model = next.(Model)
			if len(model.diagnosticWindow.entries) != 1 {
				t.Fatal("local validation failed without details")
			}
			if model.diagnosticWindow.entries[0].snapshot.Detail == "" || model.preparedInstallation != nil {
				t.Fatal("invalid local result was accepted or cause lost")
			}
		})
	}
}

func TestDiagnostics_LegacyDetailCopyFailureReachesHistory(t *testing.T) {
	model := NewModel()
	model.modal = NewErrorDetail("fixture", "original body")
	cause := errors.New("clipboard token=fixture-detail")
	model.modal.copyText = func(string) error { return cause }
	next, _ := model.Update(model.modal.copyCommand()())
	model = next.(Model)
	if len(model.diagnosticWindow.entries) != 1 || !strings.Contains(model.diagnosticWindow.entries[0].snapshot.Detail, "fixture-detail") {
		t.Fatal("copy failure lost")
	}
	if model.modal == nil || model.modal.body != "original body" || model.modal.copyStatus != "Copy failed" {
		t.Fatal("copy error discarded original modal")
	}
}

func TestDiagnostics_HistoryQueryFailureDoesNotEnterHistory(t *testing.T) {
	model := NewModel()
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "query-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	model.localDiagnosticHistory = history
	for range 2 {
		next, _ := model.Update(sessionEventMsg{Open: true, Event: session.Event{Kind: session.EventDiagnostics, DiagnosticQuery: true, Err: errors.New("history token=fixture-original")}})
		model = next.(Model)
	}
	if len(model.diagnosticWindow.entries) != 0 || len(history.List("", 0, 100).Records) != 0 {
		t.Fatal("history query failure entered diagnostic history")
	}
	next, _ := model.Update(diagnosticKey(tea.KeyF2))
	model = next.(Model)
	next, _ = model.Update(diagnosticKey(tea.KeyTab))
	model = next.(Model)
	if !strings.Contains(model.View().Content, "token=fixture-original") {
		t.Fatal("history lookup cause is not inspectable")
	}
	var copied string
	model.diagnosticWindow.copyText = func(value string) error { copied = value; return nil }
	next, copyCmd := model.Update(diagnosticKey('c'))
	model = next.(Model)
	if copyCmd == nil {
		t.Fatal("query failure cannot be copied")
	}
	next, _ = model.Update(copyCmd())
	model = next.(Model)
	if copied != "history token=fixture-original" {
		t.Fatal("query failure copy lost raw cause")
	}
	next, _ = model.Update(sessionEventMsg{Open: true, Event: session.Event{Kind: session.EventDiagnostics, DiagnosticQuery: true, Diagnostics: protocol.DiagnosticList{State: protocol.DiagnosticAvailable}}})
	model = next.(Model)
	if strings.Contains(model.View().Content, "token=fixture-original") {
		t.Fatal("successful query retained obsolete lookup error")
	}
}

func TestDiagnostics_InstallationRejectionIsInspectable(t *testing.T) {
	for _, elevated := range []bool{false, true} {
		model := NewModel()
		model.setInstallationActions(InstallationActions{Elevated: func() bool { return elevated }})
		model.installation.visible = true
		next, cmd := model.Update(diagnosticKey(tea.KeyEnter))
		model = next.(Model)
		if cmd == nil {
			t.Fatal("installation rejection has no diagnostic")
		}
		next, _ = model.Update(cmd())
		model = next.(Model)
		if len(model.diagnosticWindow.entries) != 1 || model.diagnosticWindow.entries[0].snapshot.Detail == "" || model.preparedInstallation != nil || model.modal == nil {
			t.Fatal("installation rejection lost details or changed confirmation")
		}
	}
}

func TestDiagnosticsF2_AllPagesKeepCurrentActionAheadOfNewBackgroundRecord(t *testing.T) {
	for _, page := range []ui.PageID{ui.PageSetup, ui.PageOverview, ui.PageProxies, ui.PageSubscriptions, ui.PageConnections, ui.PageRules, ui.PageLogs, ui.PageWebGUI, ui.PageSystem} {
		model := NewModel()
		model.active = page
		next, _ := model.Update(actionCompletedMsg{Intent: ui.ActionIntentMsg{Page: page, Key: "fixture-action", Title: "Fixture action"}, Result: diagnosticActionResult{failure: errors.New("token=fixture-current-action")}})
		model = next.(Model)
		newer := protocol.Diagnostic{ID: "newer-background", State: protocol.DiagnosticAvailable, Summary: "new background", Detail: "different cause", Time: time.Now().Add(time.Second)}
		model.recordDiagnostic(nil, &newer, page)
		next, _ = model.Update(diagnosticKey(tea.KeyF2))
		model = next.(Model)
		if model.diagnosticWindow.pinned.Detail != "token=fixture-current-action" {
			t.Fatalf("%s lost current action preference", page)
		}
		next, cmd := model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		model = next.(Model)
		if cmd == nil {
			t.Fatalf("%s diagnostics swallowed Ctrl+C", page)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("Ctrl+C no longer quits")
		}
	}
}
