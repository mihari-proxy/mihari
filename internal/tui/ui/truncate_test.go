package ui

import (
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestTruncateDisplay_ShortInputUnchanged(t *testing.T) {
	if got := TruncateDisplay("abcdef", 10); got != "abcdef" {
		t.Fatalf("got=%q", got)
	}
}

func TestTruncateDisplay_NonPositiveWidthEmpty(t *testing.T) {
	if got := TruncateDisplay("abc", 0); got != "" {
		t.Fatalf("got=%q", got)
	}
	if got := TruncateDisplay("abc", -1); got != "" {
		t.Fatalf("got=%q", got)
	}
}

func TestTruncateDisplay_TruncatesWithinBudget(t *testing.T) {
	tests := []struct {
		name  string
		value string
		max   int
	}{
		{name: "ascii", value: "abcdefghij", max: 6},
		{name: "cjk", value: "系统代理系统代理", max: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if lipgloss.Width(test.value) <= test.max {
				t.Fatal("fixture too short to truncate")
			}
			got := TruncateDisplay(test.value, test.max)
			if lipgloss.Width(got) > test.max {
				t.Fatalf("width=%d got=%q", lipgloss.Width(got), got)
			}
			if !containsEllipsis(got) {
				t.Fatalf("missing ellipsis: %q", got)
			}
		})
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
