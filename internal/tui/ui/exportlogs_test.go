package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func exportTestNow() time.Time {
	return time.Date(2026, 9, 2, 23, 41, 8, 0, time.FixedZone("UTC+8", 8*60*60))
}

func key(code rune, text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: text} }

func TestExportLogsModel_OpenDefaultsAndNavigation(t *testing.T) {
	now := exportTestNow()
	dir := `C:\exports`
	var probes []string
	m := NewExportLogsModel(ExportLogsOptions{
		Now:        func() time.Time { return now },
		DefaultDir: dir,
		Exists: func(gotDir, name string) (bool, error) {
			if gotDir != dir {
				t.Fatalf("dir=%q", gotDir)
			}
			probes = append(probes, name)
			return len(probes) == 1, nil
		},
	})
	if !m.Closed() {
		t.Fatal("new model must be closed")
	}
	m.Open()
	view := m.View(300, 30)
	for _, want := range []string{"Export Logs", "2026-09-02 23:41:08 +08:00", "Last 24 hours", filepath.Join(dir, "mihari-logs-20260902-234108+0800-1.zip")} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	if got := probes; len(got) != 2 || got[0] != "mihari-logs-20260902-234108+0800.zip" || got[1] != "mihari-logs-20260902-234108+0800-1.zip" {
		t.Fatalf("probes=%v", got)
	}

	// Range is initially focused and Enter cycles all choices.
	for _, want := range []string{"Last 60 minutes", "Between", "All", "Last 24 hours"} {
		_, consumed := m.Update(key(tea.KeyEnter, ""))
		if !consumed || !strings.Contains(m.View(100, 30), want) {
			t.Fatalf("range did not cycle to %q", want)
		}
	}
	// Entering Between initializes its two local-time fields.
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyEnter, ""))
	between := m.View(100, 30)
	if !strings.Contains(between, "2026-09-01 23:41") || !strings.Contains(between, "2026-09-02 23:41") {
		t.Fatalf("between defaults:\n%s", between)
	}
	for i := 0; i < 3; i++ {
		if _, ok := m.Update(key(tea.KeyTab, "")); !ok {
			t.Fatal("tab not consumed")
		}
	}
	m.focus = exportFocusRange
	if _, ok := m.Update(key(tea.KeyTab, "")); !ok || m.focus != exportFocusFrom {
		t.Fatalf("tab focus=%v want From", m.focus)
	}
	if _, ok := m.Update(key(tea.KeyTab, "")); !ok || m.focus != exportFocusTo {
		t.Fatalf("second tab focus=%v want To", m.focus)
	}
	if _, ok := m.Update(key(tea.KeyTab, "")); !ok || m.focus != exportFocusOutput {
		t.Fatalf("third tab focus=%v want Output", m.focus)
	}
	if _, ok := m.Update(key(tea.KeyTab, "")); !ok || m.focus != exportFocusRange {
		t.Fatalf("tab wrap focus=%v want Range", m.focus)
	}
	if _, ok := m.Update(key(tea.KeyTab, "")); !ok || m.focus != exportFocusFrom {
		t.Fatalf("tab focus=%v want From", m.focus)
	}
	if _, ok := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}); !ok || m.focus != exportFocusRange {
		t.Fatalf("shift+tab focus=%v want Range", m.focus)
	}
	m.Update(key(tea.KeyTab, ""))
	if _, ok := m.Update(tea.PasteMsg{Content: "chosen.zip"}); !ok {
		t.Fatal("focused paste not consumed")
	}
	if _, ok := m.Update(struct{ X int }{1}); ok {
		t.Fatal("unknown message consumed")
	}
	if _, ok := m.Update(key(tea.KeyEsc, "")); !ok || !m.Closed() {
		t.Fatal("esc did not close editable dialog")
	}
	if _, ok := m.Update(key('x', "x")); ok {
		t.Fatal("closed dialog consumed key")
	}
	if _, ok := m.Update(OpenExportLogsMsg{}); !ok || m.Closed() {
		t.Fatal("open message did not open dialog")
	}
}

func TestExportLogsModel_SubmitRecomputesDefaultAndRange(t *testing.T) {
	opened := exportTestNow()
	submitted := opened.Add(2 * time.Minute)
	times := []time.Time{opened, submitted}
	dir := t.TempDir()
	requests := make(chan logging.ExportRequest, 1)
	m := NewExportLogsModel(ExportLogsOptions{
		Now: func() time.Time { now := times[0]; times = times[1:]; return now }, DefaultDir: dir,
		Exists: func(_ string, name string) (bool, error) { return strings.HasSuffix(name, "-234308+0800.zip"), nil },
		Export: func(_ context.Context, request logging.ExportRequest) (logging.ExportResult, error) {
			requests <- request
			return logging.ExportResult{Path: filepath.Join(dir, "published.zip")}, nil
		},
	})
	m.Open()
	cmd, consumed := m.Update(key(tea.KeyTab, "")) // output field
	if cmd != nil || !consumed {
		t.Fatal("tab")
	}
	cmd, consumed = m.Update(key(tea.KeyEnter, ""))
	if !consumed || cmd == nil || !m.Pending() {
		t.Fatal("submit did not start synchronously")
	}
	req := <-requests
	if !req.AutoNumber || req.OutputPath != "" {
		t.Fatalf("default target request=%+v", req)
	}
	if req.Now != submitted || req.Range.Kind != logging.RangeLast24Hours || !req.Range.To.Equal(submitted.UTC()) || !req.Range.From.Equal(submitted.Add(-24*time.Hour).UTC()) {
		t.Fatalf("range=%+v now=%v", req.Range, req.Now)
	}
	msg := cmd()
	m.Update(msg)
	if m.Pending() || !strings.Contains(m.View(300, 30), "Export complete") {
		t.Fatal("success not shown")
	}
}

func TestExportLogsModel_CustomBetweenValidationAndRequest(t *testing.T) {
	now := exportTestNow()
	out := filepath.Join(t.TempDir(), "chosen.zip")
	requests := make(chan logging.ExportRequest, 1)
	m := NewExportLogsModel(ExportLogsOptions{Now: func() time.Time { return now }, DefaultDir: t.TempDir(), Export: func(_ context.Context, r logging.ExportRequest) (logging.ExportResult, error) {
		requests <- r
		return logging.ExportResult{Path: out}, nil
	}})
	m.Open()
	m.Update(key(tea.KeyEnter, ""))
	m.Update(key(tea.KeyEnter, "")) // Between
	// Focus From, replace internals to exercise strict parser and retained parameters.
	m.focus = exportFocusFrom
	m.from = "2026-09-03 00:00"
	m.to = "2026-09-02 23:59"
	m.output = out
	if cmd, _ := m.Update(key(tea.KeyEnter, "")); cmd != nil || !strings.Contains(m.View(100, 30), "From must not be after To") {
		t.Fatal("invalid interval accepted")
	}
	if m.from != "2026-09-03 00:00" || m.to != "2026-09-02 23:59" {
		t.Fatal("invalid parameters changed")
	}
	m.from = "2026-09-02 22:00"
	m.to = "2026-09-02 23:00"
	cmd, _ := m.Update(key(tea.KeyEnter, ""))
	req := <-requests
	if req.AutoNumber || req.OutputPath != out || req.Range.From.Location() != time.UTC || req.Range.From.Hour() != 14 || req.Range.To.Hour() != 15 {
		t.Fatalf("request=%+v", req)
	}
	m.Update(cmd())
}

func TestExportLogsModel_BetweenRejectsMalformedAndNoncanonicalTimes(t *testing.T) {
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow, DefaultDir: t.TempDir()})
	cases := []struct {
		name string
		from string
		to   string
	}{
		{name: "malformed", from: "not-a-time", to: "2026-09-02 23:41"},
		{name: "noncanonical", from: "2026-9-2 3:04", to: "2026-09-02 23:41"},
		{name: "seconds", from: "2026-09-02 03:04:05", to: "2026-09-02 23:41"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m.Open()
			m.rangeKind, m.focus, m.from, m.to = logging.RangeBetween, exportFocusFrom, tc.from, tc.to
			if cmd, consumed := m.Update(key(tea.KeyEnter, "")); !consumed || cmd != nil {
				t.Fatalf("submit=(%v,%v)", cmd, consumed)
			}
			if !strings.Contains(m.View(100, 30), ExportTimeInvalid) || m.from != tc.from || m.to != tc.to {
				t.Fatalf("invalid input changed or error absent: from=%q to=%q", m.from, m.to)
			}
		})
	}
}

func TestExportLogsModel_PendingCancelAndStaleResult(t *testing.T) {
	started := make(chan struct{})
	exited := make(chan struct{})
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow, DefaultDir: t.TempDir(), Export: func(ctx context.Context, _ logging.ExportRequest) (logging.ExportResult, error) {
		close(started)
		<-ctx.Done()
		close(exited)
		return logging.ExportResult{}, ctx.Err()
	}})
	m.Open()
	m.Update(key(tea.KeyTab, ""))
	cmd, _ := m.Update(key(tea.KeyEnter, ""))
	<-started
	before := m.output
	for _, msg := range []tea.Msg{key(tea.KeyTab, ""), tea.PasteMsg{Content: "ignored"}, key(tea.KeyEnter, "")} {
		if c, ok := m.Update(msg); !ok || c != nil {
			t.Fatal("pending input not exclusively consumed")
		}
	}
	if m.output != before {
		t.Fatal("pending edit changed output")
	}
	m.Update(exportResultMsg{Generation: m.generation - 1, Err: errors.New("stale")})
	if !m.Pending() {
		t.Fatal("stale result cleared pending")
	}
	m.Update(key(tea.KeyEsc, ""))
	if !m.Pending() {
		t.Fatal("esc cleared pending before cleanup")
	}
	<-exited
	m.Update(cmd())
	if m.Pending() || !strings.Contains(m.View(100, 30), "Export cancelled") {
		t.Fatal("cancel result not shown")
	}
}

func TestExportLogsModel_CancelRequestDoesNotHidePublishedSuccess(t *testing.T) {
	out := `C:\published.zip`
	var copied string
	m := NewExportLogsModel(ExportLogsOptions{
		Now: exportTestNow, DefaultDir: t.TempDir(),
		WriteClipboard: func(path string) error { copied = path; return nil },
	})
	m.Open()
	m.pending, m.generation = true, 7
	m.Update(key(tea.KeyEsc, ""))
	if !m.Pending() {
		t.Fatal("Esc must leave the model pending until result delivery")
	}
	m.Update(exportResultMsg{Generation: 7, Result: logging.ExportResult{Path: out}})
	view := m.View(100, 30)
	if m.Pending() || !strings.Contains(view, ExportComplete) || !strings.Contains(view, out) || strings.Contains(view, ExportCancelled) {
		t.Fatalf("successful published result was hidden:\n%s", view)
	}
	m.Update(key(tea.KeyEnter, ""))
	if copied != out {
		t.Fatalf("copied=%q want %q", copied, out)
	}
}

func TestExportLogsModel_ReopenKeepsGenerationMonotonicAcrossSuccesses(t *testing.T) {
	call := 0
	m := NewExportLogsModel(ExportLogsOptions{
		Now: exportTestNow, DefaultDir: t.TempDir(),
		Export: func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
			call++
			return logging.ExportResult{Path: fmt.Sprintf(`C:\export-%d.zip`, call)}, nil
		},
	})
	var previous uint64
	for wantCall := 1; wantCall <= 2; wantCall++ {
		m.Open()
		m.Update(key(tea.KeyTab, ""))
		cmd, _ := m.Update(key(tea.KeyEnter, ""))
		if cmd == nil {
			t.Fatalf("submission %d missing command", wantCall)
		}
		generation := m.generation
		if generation <= previous {
			t.Fatalf("generation=%d previous=%d", generation, previous)
		}
		m.Update(cmd())
		if !strings.Contains(m.View(100, 30), fmt.Sprintf(`C:\export-%d.zip`, wantCall)) {
			t.Fatalf("submission %d success missing", wantCall)
		}
		m.Update(key(tea.KeyEsc, ""))
		if !m.Closed() {
			t.Fatalf("submission %d did not close", wantCall)
		}
		previous = generation
	}
}

func TestExportLogsModel_CancelAndWaitBeforeCommandRuns(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	observed := make(chan struct{})
	m := NewExportLogsModel(ExportLogsOptions{Context: parent, Now: exportTestNow, DefaultDir: t.TempDir(), Export: func(ctx context.Context, _ logging.ExportRequest) (logging.ExportResult, error) {
		<-ctx.Done()
		close(observed)
		return logging.ExportResult{}, ctx.Err()
	}})
	m.Open()
	m.Update(key(tea.KeyTab, ""))
	cmd, _ := m.Update(key(tea.KeyEnter, ""))
	if cmd == nil {
		t.Fatal("missing waiter command")
	}
	cancel()
	done := make(chan struct{})
	go func() { m.CancelAndWait(); close(done) }()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("export did not observe cancel")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("CancelAndWait blocked")
	}
	// The buffered result must also let a late waiter finish.
	waited := make(chan tea.Msg, 1)
	go func() { waited <- cmd() }()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("result waiter blocked")
	}
}

func TestExportRunner_RejectsConcurrentStartAndRecoversPanic(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	r := newExportRunner(nil, func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
		if calls.Add(1) == 1 {
			<-release
			panic("secret panic")
		}
		return logging.ExportResult{Path: "ok"}, nil
	})
	result, ok := r.Start(1, logging.ExportRequest{})
	if !ok {
		t.Fatal("first start rejected")
	}
	if _, ok := r.Start(2, logging.ExportRequest{}); ok {
		t.Fatal("concurrent start accepted")
	}
	close(release)
	msg := <-result
	if msg.Generation != 1 || msg.Err == nil || strings.Contains(msg.Err.Error(), "secret panic") {
		t.Fatalf("panic result=%+v", msg)
	}
	r.CancelAndWait()
	result, ok = r.Start(2, logging.ExportRequest{})
	if !ok {
		t.Fatal("second sequential start rejected")
	}
	if msg = <-result; msg.Err != nil || msg.Result.Path != "ok" {
		t.Fatalf("second=%+v", msg)
	}
	r.CancelAndWait()
}

func TestExportLogsModel_SuccessCopyAndSanitizedErrors(t *testing.T) {
	out := `C:\done.zip`
	var copied string
	errCopy := errors.New("clipboard unavailable")
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow, DefaultDir: t.TempDir(), WriteClipboard: func(path string) error { copied = path; return errCopy }})
	m.Open()
	m.pending = true
	m.generation = 4
	m.Update(exportResultMsg{Generation: 4, Result: logging.ExportResult{Path: out}})
	if view := m.View(300, 30); !strings.Contains(view, "Export complete") || !strings.Contains(view, out) || !strings.Contains(view, "Enter copy path  Esc close") {
		t.Fatalf("success view:\n%s", view)
	}
	m.Update(key(tea.KeyEnter, ""))
	if copied != out || !strings.Contains(m.View(100, 30), "Could not copy path") {
		t.Fatal("copy failure behavior")
	}
	m.Update(key(tea.KeyEsc, ""))
	if !m.Closed() {
		t.Fatal("success esc did not close")
	}

	cases := []struct {
		err  error
		want string
	}{
		{logging.ErrNoLogLines, "No log lines in the selected range"},
		{fmt.Errorf("%w: C:/secret/token", logging.ErrInvalidExportRequest), "Invalid export destination"},
		{fmt.Errorf("%w: C:/secret/token", logging.ErrExportTargetExists), "Export file already exists"},
		{errors.New("open C:/secret/token: denied"), "Could not export logs"},
	}
	for i, tc := range cases {
		m.Open()
		m.pending = true
		m.generation = uint64(i + 10)
		m.Update(exportResultMsg{Generation: m.generation, Err: tc.err})
		view := m.View(100, 30)
		if !strings.Contains(view, tc.want) || strings.Contains(view, "secret") || strings.Contains(view, "token") {
			t.Fatalf("err=%v view=%s", tc.err, view)
		}
	}
}

func TestExportLogsModel_QuitKeysAreNotConsumedOutsideText(t *testing.T) {
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow, DefaultDir: t.TempDir()})
	m.Open()
	for _, msg := range []tea.Msg{key('q', "q"), tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}} {
		if _, consumed := m.Update(msg); consumed {
			t.Fatalf("quit key consumed: %#v", msg)
		}
	}
	m.focus = exportFocusOutput
	if _, consumed := m.Update(key('q', "q")); !consumed || !strings.Contains(m.output, "q") {
		t.Fatal("text q not edited")
	}
}
