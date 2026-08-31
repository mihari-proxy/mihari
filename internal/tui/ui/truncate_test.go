package ui

import (
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestTruncateDisplay_ASCIIAndCJKStayWithinBudget(t *testing.T) {
	if got := TruncateDisplay("abcdef", 10); got != "abcdef" {
		t.Fatalf("short ASCII=%q", got)
	}
	got := TruncateDisplay("abcdefghij", 6)
	if lipgloss.Width(got) > 6 {
		t.Fatalf("ASCII width=%d got=%q", lipgloss.Width(got), got)
	}
	if !containsEllipsis(got) {
		t.Fatalf("ASCII truncated without ellipsis: %q", got)
	}

	cjk := "系统代理系统代理"
	got = TruncateDisplay(cjk, 7)
	if lipgloss.Width(got) > 7 {
		t.Fatalf("CJK width=%d got=%q", lipgloss.Width(got), got)
	}
	if lipgloss.Width(cjk) <= 7 {
		t.Fatal("fixture too short to exercise CJK truncation")
	}
	if TruncateDisplay("abc", 0) != "" {
		t.Fatal("max<=0 should be empty")
	}
}

func containsEllipsis(value string) bool {
	for _, r := range value {
		if r == '…' {
			return true
		}
	}
	return false
}
