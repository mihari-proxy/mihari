package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestSearchBar_ViewShowsQuery(t *testing.T) {
	theme := DefaultTheme()

	empty := RenderSearchBar(theme, "", "Search connections…", false, 0, 80)
	plainEmpty := stripANSI(empty)
	if !strings.Contains(plainEmpty, "/") {
		t.Fatalf("search bar should prefix /:\n%s", plainEmpty)
	}
	if !strings.Contains(plainEmpty, "Search connections…") {
		t.Fatalf("empty query should show placeholder:\n%s", plainEmpty)
	}

	withQuery := RenderSearchBar(theme, "example.com", "Search…", true, 11, 80)
	plainQuery := stripANSI(withQuery)
	if !strings.Contains(plainQuery, "example.com") {
		t.Fatalf("focused bar should show query:\n%s", plainQuery)
	}
	if strings.Contains(plainQuery, "Search…") {
		t.Fatalf("non-empty query should not show placeholder:\n%s", plainQuery)
	}

	// Width 0 skips MaxWidth so style equality is exact.
	focused := RenderSearchBar(theme, "q", "Search…", true, 1, 0)
	unfocused := RenderSearchBar(theme, "q", "Search…", false, 0, 0)
	if focused == unfocused {
		t.Fatalf("focused and unfocused styles should differ")
	}
	if !strings.Contains(stripANSI(focused), "/ q") {
		t.Fatalf("focused bar should show query: %q", stripANSI(focused))
	}
	if want := theme.Muted.Render("/ q"); unfocused != want {
		t.Fatalf("unfocused bar should use Muted\ngot=%q\nwant=%q", unfocused, want)
	}
}

func TestSearchBar_FocusedShowsCursor(t *testing.T) {
	theme := DefaultTheme()
	// Cursor mid-query: reverse-video on that rune.
	got := RenderSearchBar(theme, "ab", "Search…", true, 1, 0)
	// Unfocused never embeds reverse.
	plain := stripANSI(got)
	if !strings.Contains(plain, "ab") {
		t.Fatalf("query missing: %q", plain)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("focused bar should embed style codes for cursor: %q", got)
	}
	// Cursor at end appends a reverse space.
	atEnd := RenderSearchBar(theme, "ab", "Search…", true, 2, 0)
	if lipgloss.Width(stripANSI(atEnd)) < lipgloss.Width(stripANSI(got)) {
		t.Fatalf("end cursor should add a cell: end=%q mid=%q", stripANSI(atEnd), stripANSI(got))
	}
}

func TestSearchBar_TruncatesToWidth(t *testing.T) {
	theme := DefaultTheme()
	const width = 12
	got := RenderSearchBar(theme, "a-very-long-search-query", "Search…", false, 0, width)
	if w := lipgloss.Width(got); w > width {
		t.Fatalf("width=%d exceeds max %d: %q", w, width, stripANSI(got))
	}
}
