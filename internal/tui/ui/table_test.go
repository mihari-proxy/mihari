package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestFitColumnWidths_RespectsMinMaxAndFlex(t *testing.T) {
	cols := []TableColumn{
		{ID: "a", MinWidth: 4, MaxWidth: 8, Flex: 0},
		{ID: "b", MinWidth: 6, Flex: 1},
		{ID: "c", MinWidth: 4, Flex: 1},
	}
	// total 30, gap 2 between 3 cols => 4 usable for gaps, 26 for content.
	// fixed a=4, remaining 22 split flex 1:1 => b=11 c=11? Wait min b=6 c=4, remaining after mins: 26-4-6-4=12, +flex => b=6+6 c=4+6
	got := FitColumnWidths(cols, 30, 2)
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0] != 4 {
		t.Fatalf("a width=%d", got[0])
	}
	if got[1]+got[2] != 22 {
		t.Fatalf("flex sum=%d want 22 (%v)", got[1]+got[2], got)
	}
	if got[1] < 6 || got[2] < 4 {
		t.Fatalf("mins violated: %v", got)
	}
}

func TestPadCell_LeftRightAndTruncate(t *testing.T) {
	left := PadCell("ab", 5, AlignLeft)
	if lipgloss.Width(left) != 5 || !strings.HasPrefix(left, "ab") {
		t.Fatalf("left=%q", left)
	}
	right := PadCell("12", 5, AlignRight)
	if lipgloss.Width(right) != 5 || !strings.HasSuffix(right, "12") {
		t.Fatalf("right=%q", right)
	}
	trunc := PadCell("abcdefgh", 5, AlignLeft)
	if lipgloss.Width(trunc) != 5 {
		t.Fatalf("trunc width=%d %q", lipgloss.Width(trunc), trunc)
	}
	if !strings.HasSuffix(trunc, "…") && !strings.Contains(trunc, "…") {
		t.Fatalf("expected ellipsis: %q", trunc)
	}
}

func TestRenderHeaderRow_UsesTableHeaderAndSeparator(t *testing.T) {
	theme := DefaultTheme()
	header, rule := RenderHeaderRow(theme, []string{"Time", "Level", "Message"}, []int{8, 7, 20}, 2, -1, false)
	if lipgloss.Width(header) == 0 {
		t.Fatal("empty header")
	}
	if !strings.Contains(rule, "─") {
		t.Fatalf("rule=%q", rule)
	}
	// Unfocused header should not inject reverse focus styles beyond TableHeader.
	if strings.Contains(header, "Time") == false {
		t.Fatalf("header=%q", header)
	}
}

func TestTruncateVisible_AnsiStyled(t *testing.T) {
	// Style prefixes must not consume width budget, and the SGR state must be
	// closed at the cut point (design R1).
	const blue = "\x1b[38;5;75m" // StyleRuleType colors the type in info blue.
	styled := blue + "DOMAIN-SUFFIX" + "\x1b[0m"
	got := TruncateVisible(styled, 10)
	// 10 columns: "DOMAIN-SU…" keeps full color for the visible runes.
	if lipgloss.Width(got) != 10 {
		t.Fatalf("width=%d %q", lipgloss.Width(got), got)
	}
	if !strings.HasPrefix(got, blue+"DOMAIN-SU") {
		t.Fatalf("lost style prefix: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("no ellipsis: %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("SGR not closed: %q", got)
	}
	// Bare ellipsis when the budget is one column.
	if one := TruncateVisible(styled, 1); one != "…\x1b[0m" {
		t.Fatalf("width 1 = %q", one)
	}
	// No truncation keeps the string verbatim.
	if full := TruncateVisible(styled, 30); full != styled {
		t.Fatalf("no-truncate changed it: %q", full)
	}
}

func TestTruncateVisible_MultiSegmentStyles(t *testing.T) {
	// ↑/↓ two-color traffic pair: each segment keeps its own prefix.
	const up = "\x1b[38;5;78m"
	const down = "\x1b[38;5;214m"
	pair := up + "↑1.0 MiB/s" + "\x1b[0m" + "  " + down + "↓2.0 KiB/s" + "\x1b[0m"
	got := TruncateVisible(pair, 18)
	if lipgloss.Width(got) != 18 {
		t.Fatalf("width=%d %q", lipgloss.Width(got), got)
	}
	if !strings.Contains(got, up) || !strings.Contains(got, down) {
		t.Fatalf("segment styles lost: %q", got)
	}
}

func TestTruncateVisible_CJKWidth(t *testing.T) {
	// CJK chars are double-width: 4 CJK + "…" = 9 columns.
	got := TruncateVisible("中文测试abcd", 9)
	if lipgloss.Width(got) != 9 {
		t.Fatalf("width=%d %q", lipgloss.Width(got), got)
	}
	if got != "中文测试…" {
		t.Fatalf("got %q", got)
	}
	// A double-width char at the boundary is not split: "中文测…" = 7 columns.
	boundary := TruncateVisible("中文测试abcd", 8)
	if lipgloss.Width(boundary) != 7 || strings.Contains(boundary, "测试") {
		t.Fatalf("boundary=%q (width %d)", boundary, lipgloss.Width(boundary))
	}
}

func TestStyleLogLevel_MapsSemanticColors(t *testing.T) {
	theme := DefaultTheme()
	errorStyled := StyleLogLevel(theme, "error")
	warnStyled := StyleLogLevel(theme, "warning")
	infoStyled := StyleLogLevel(theme, "info")
	debugStyled := StyleLogLevel(theme, "debug")
	if errorStyled == "ERROR" || !strings.Contains(errorStyled, "\x1b[") {
		t.Fatalf("error should be styled: %q", errorStyled)
	}
	if warnStyled == "WARNING" || !strings.Contains(warnStyled, "\x1b[") {
		t.Fatalf("warn should be styled: %q", warnStyled)
	}
	if infoStyled == "INFO" || !strings.Contains(infoStyled, "\x1b[") {
		t.Fatalf("info should be styled: %q", infoStyled)
	}
	if debugStyled == "DEBUG" || !strings.Contains(debugStyled, "\x1b[") {
		t.Fatalf("debug should be muted styled: %q", debugStyled)
	}
}

func TestStyleProxyTarget_AndProviderStatus(t *testing.T) {
	theme := DefaultTheme()
	direct := StyleProxyTarget(theme, "DIRECT")
	reject := StyleProxyTarget(theme, "REJECT")
	proxy := StyleProxyTarget(theme, "Proxy")
	if !strings.Contains(direct, "\x1b[") || !strings.Contains(reject, "\x1b[") || !strings.Contains(proxy, "\x1b[") {
		t.Fatalf("targets should be colored: %q %q %q", direct, reject, proxy)
	}
	ready := StyleProviderStatus(theme, "Ready")
	failed := StyleProviderStatus(theme, "Failed")
	if !strings.Contains(ready, "\x1b[") || !strings.Contains(failed, "\x1b[") {
		t.Fatalf("status should be colored: %q %q", ready, failed)
	}
}

func TestClassifyContentWidth(t *testing.T) {
	if ClassifyContentWidth(80) != ContentCompact {
		t.Fatal("80 should be compact")
	}
	if ClassifyContentWidth(90) != ContentFull {
		t.Fatal("90 should be full")
	}
}
