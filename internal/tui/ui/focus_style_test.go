package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestApplyFocusStyle_PlainText(t *testing.T) {
	focus := lipgloss.NewStyle().Reverse(true)
	if got := ApplyFocusStyle("hello world", focus); got != "\x1b[7mhello world\x1b[m" {
		t.Fatalf("got %q", got)
	}
	if got := ApplyFocusStyle("", focus); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

func TestApplyFocusStyle_WrapsEachPlainSegment(t *testing.T) {
	focus := lipgloss.NewStyle().Reverse(true)
	// Inline color span splits the line into three plain segments; each must be
	// reverse-wrapped and the escape sequences must stay verbatim in input order.
	input := "\x1b[38;5;75mblue\x1b[m tail"
	want := "\x1b[38;5;75m" + "\x1b[7mblue\x1b[m" + "\x1b[m" + "\x1b[7m tail\x1b[m"
	if got := ApplyFocusStyle(input, focus); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestApplyFocusStyle_StyleTrafficPairSpans(t *testing.T) {
	focus := lipgloss.NewStyle().Reverse(true)
	theme := DefaultTheme()
	input := StyleTrafficPair(theme, "↑1.0 KiB/s", "↓2.0 KiB/s")
	// Segments: "↑1.0 KiB/s", "  ", "↓2.0 KiB/s"; the 78/75 color prefixes and
	// the two resets stay verbatim in input order around them.
	want := "\x1b[38;5;78m" + "\x1b[7m↑1.0 KiB/s\x1b[m" + "\x1b[m" +
		"\x1b[7m  \x1b[m" +
		"\x1b[38;5;75m" + "\x1b[7m↓2.0 KiB/s\x1b[m" + "\x1b[m"
	if got := ApplyFocusStyle(input, focus); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestApplyFocusStyle_UnterminatedTrailingEscape(t *testing.T) {
	focus := lipgloss.NewStyle().Reverse(true)
	input := "abc\x1b[38;5;75" // truncated CSI, no final byte
	want := "\x1b[7mabc\x1b[m" + "\x1b[38;5;75"
	if got := ApplyFocusStyle(input, focus); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestApplyFocusStyle_EscapeOnlyInput(t *testing.T) {
	focus := lipgloss.NewStyle().Reverse(true)
	if got := ApplyFocusStyle("\x1b[m", focus); got != "\x1b[m" {
		t.Fatalf("escape-only: got %q", got)
	}
}

func TestApplyFocusStyle_MultibyteSegments(t *testing.T) {
	focus := lipgloss.NewStyle().Reverse(true)
	input := "中文\x1b[31m红\x1b[m字"
	want := "\x1b[7m中文\x1b[m" + "\x1b[31m" + "\x1b[7m红\x1b[m" + "\x1b[m" + "\x1b[7m字\x1b[m"
	if got := ApplyFocusStyle(input, focus); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestApplyFocusStyle_PlainTextMatchesFocusRender(t *testing.T) {
	focus := lipgloss.NewStyle().Reverse(true)
	text := "no escapes here"
	if got := ApplyFocusStyle(text, focus); got != focus.Render(text) {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(ApplyFocusStyle(text, focus), "\x1b[m\x1b[7m") {
		t.Fatal("adjacent segments should not double-wrap")
	}
}
