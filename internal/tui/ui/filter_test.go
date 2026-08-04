package ui

import "testing"

func TestMatchVisibleColumns_OnlyListedCells(t *testing.T) {
	cells := map[string]string{
		"host":   "example.com",
		"chain":  "DIRECT",
		"hidden": "secret-token",
	}
	visible := []string{"host", "chain"}

	if !MatchVisibleColumns(cells, visible, "example") {
		t.Fatal("expected host match")
	}
	if !MatchVisibleColumns(cells, visible, "EXAMPLE") {
		t.Fatal("match should be case-insensitive")
	}
	if !MatchVisibleColumns(cells, visible, "direct") {
		t.Fatal("expected chain match")
	}
	if MatchVisibleColumns(cells, visible, "secret") {
		t.Fatal("hidden column must not match")
	}
	if !MatchVisibleColumns(cells, visible, "") {
		t.Fatal("empty query must match all")
	}
	if !MatchVisibleColumns(cells, visible, "   ") {
		t.Fatal("whitespace-only query must match all")
	}
	if MatchVisibleColumns(cells, visible, "no-such-value") {
		t.Fatal("non-matching query must return false")
	}

	// Visible key missing from cells is skipped, not a panic.
	if MatchVisibleColumns(map[string]string{"host": "x"}, []string{"host", "missing"}, "missing") {
		t.Fatal("absent visible column value must not invent a match")
	}
}
