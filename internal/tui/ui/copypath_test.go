package ui

import (
	"errors"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func TestCopyPath_SSHSendsToTerminalWithoutWriting(t *testing.T) {
	called := false
	got := CopyPath(CopyRequest{
		Text:     "fixture-path",
		SSH:      true,
		Terminal: true,
		Write: func(string) error {
			called = true
			return nil
		},
	})
	if called || got.Kind != CopySentToTerminal || got.Err != nil || got.Command == nil {
		t.Fatalf("called=%v result=%+v", called, got)
	}
	if msg := got.Command(); msg == nil {
		t.Fatal("terminal copy produced no message")
	}
}

func TestCopyPath_SSHWithoutTerminalFails(t *testing.T) {
	called := false
	got := CopyPath(CopyRequest{
		Text:     "fixture-path",
		SSH:      true,
		Terminal: false,
		Write: func(string) error {
			called = true
			return nil
		},
	})
	if called || got.Kind != CopyFailed || !errors.Is(got.Err, ErrCopyNoTerminal) || got.Command != nil {
		t.Fatalf("called=%v result=%+v", called, got)
	}
	if text := CopyFailureText(ExportCopyFailed, got.Err); text != "Could not copy path: the terminal cannot receive a copy request" {
		t.Fatalf("text=%q", text)
	}
}

func TestCopyPath_FeedbackSuccess(t *testing.T) {
	var gotText string
	got := CopyPath(CopyRequest{Text: "fixture-path", Write: func(text string) error {
		gotText = text
		return nil
	}})
	if gotText != "fixture-path" || got.Kind != CopyConfirmed || got.Err != nil || got.Command != nil {
		t.Fatalf("text=%q result=%+v", gotText, got)
	}
}

func TestCopyPath_FeedbackFailureKeepsCause(t *testing.T) {
	cause := errors.New("wl-copy: fixture unavailable")
	got := CopyPath(CopyRequest{Text: "fixture-path", Write: func(string) error { return cause }})
	if got.Kind != CopyFailed || !errors.Is(got.Err, cause) || got.Command != nil {
		t.Fatalf("result=%+v", got)
	}
	text := CopyFailureText(ExportCopyFailed, got.Err)
	if text != "Could not copy path: wl-copy: fixture unavailable" {
		t.Fatalf("text=%q", text)
	}
	escaped := CopyFailureText(ExportCopyFailed, errors.New("bad\x1b]52;c;fixture\a path"))
	if strings.Contains(escaped, "\x1b") || !strings.Contains(escaped, "Could not copy path:") || !strings.Contains(escaped, "fixture") {
		t.Fatalf("escaped=%q", escaped)
	}
}

func TestExportLogsModel_CopySentUsesTerminalNotice(t *testing.T) {
	const path = "fixture-export.zip"
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow, DefaultDir: t.TempDir(), Copy: func(text string) CopyResult {
		if text != path {
			t.Fatalf("copied %q", text)
		}
		return CopyResult{Kind: CopySentToTerminal, Command: func() tea.Msg { return tea.BatchMsg{} }}
	}})
	m.Open()
	m.pending = true
	m.Update(exportResultMsg{Generation: m.generation, Result: logging.ExportResult{Path: path}})
	_, consumed := m.Update(key(tea.KeyEnter, ""))
	view := m.View(160, 30)
	theme := DefaultTheme()
	for _, line := range CopySentLines() {
		if !consumed || !strings.Contains(view, theme.BrightYellow.Render(line)) {
			t.Fatalf("sent notice missing bright line %q:\n%s", line, view)
		}
	}
	kept := false
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, CopyManualHint) {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("manual hint broke before if:\n%s", ansi.Strip(view))
	}
	if !strings.Contains(CopySentNotice, "OSC 52") || !strings.Contains(CopySentNotice, "clipboard command") || !strings.Contains(CopyManualHint, "if your terminal is not receiving OSC 52.") {
		t.Fatalf("notice=%q hint=%q", CopySentNotice, CopyManualHint)
	}
	if strings.Contains(view, theme.Muted.Render(CopySentNotice)) || strings.Contains(view, theme.Success.Render(CopySentNotice)) {
		t.Fatalf("sent notice used the wrong color:\n%s", view)
	}
	if strings.Contains(view, ExportPathCopied) || strings.Contains(view, ExportCopyFailed) {
		t.Fatalf("sent notice claimed a confirmed result:\n%s", view)
	}
}

func TestExportLogsModel_CopyFailureShowsCause(t *testing.T) {
	cause := errors.New("wl-copy: fixture unavailable")
	m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow, DefaultDir: t.TempDir(), WriteClipboard: func(string) error { return cause }})
	m.Open()
	m.pending = true
	m.Update(exportResultMsg{Generation: m.generation, Result: logging.ExportResult{Path: "fixture-export.zip"}})
	m.Update(key(tea.KeyEnter, ""))
	want := "Could not copy path: wl-copy: fixture unavailable"
	view := m.View(180, 30)
	if !strings.Contains(view, DefaultTheme().Danger.Render(want)) {
		t.Fatalf("failure notice missing:\n%s", view)
	}
}

func TestSSHDetectionAndUserWaylandCommand(t *testing.T) {
	if sshFromEnviron(func(string) string { return "" }) {
		t.Fatal("empty environment looked like SSH")
	}
	if !sshFromEnviron(func(key string) string {
		if key == "SSH_CONNECTION" {
			return "203.0.113.5 1 192.0.2.1 22"
		}
		return ""
	}) {
		t.Fatal("SSH_CONNECTION was ignored")
	}
	procs := map[int]struct {
		comm string
		ppid int
	}{40: {"sudo", 30}, 30: {"bash", 20}, 20: {"sshd", 1}}
	read := func(pid int) (string, int, error) {
		item := procs[pid]
		return item.comm, item.ppid, nil
	}
	if !sshAncestorFrom(40, read) {
		t.Fatal("sudo under sshd was not an SSH session")
	}
	if sshAncestorFrom(30, func(int) (string, int, error) { return "bash", 1, nil }) {
		t.Fatal("local shell looked like SSH")
	}
	argv, ok := userWaylandArgs(0, "1000", "kinema", "wayland-0", "/usr/bin/wl-copy", "/usr/sbin/runuser")
	if !ok || strings.Join(argv, " ") != "/usr/sbin/runuser -u kinema -- env XDG_RUNTIME_DIR=/run/user/1000 WAYLAND_DISPLAY=wayland-0 /usr/bin/wl-copy" {
		t.Fatalf("argv=%q ok=%v", argv, ok)
	}
	if _, ok := userWaylandArgs(0, "1000", "bad name", "wayland-0", "/usr/bin/wl-copy", "/usr/sbin/runuser"); ok {
		t.Fatal("account name with a space was accepted")
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/wayland-0.lock", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/wayland-1", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := waylandDisplay(dir); got != "wayland-1" {
		t.Fatalf("display=%q", got)
	}
}
