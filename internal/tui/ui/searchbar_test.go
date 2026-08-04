package ui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
)

func TestSearchBar_ViewShowsQuery(t *testing.T) {
	theme := DefaultTheme()

	empty := RenderSearchBar(theme, "", "Search connections…", false, 80)
	plainEmpty := stripANSI(empty)
	if !strings.Contains(plainEmpty, "/") {
		t.Fatalf("search bar should prefix /:\n%s", plainEmpty)
	}
	if !strings.Contains(plainEmpty, "Search connections…") {
		t.Fatalf("empty query should show placeholder:\n%s", plainEmpty)
	}

	withQuery := RenderSearchBar(theme, "example.com", "Search…", true, 80)
	plainQuery := stripANSI(withQuery)
	if !strings.Contains(plainQuery, "example.com") {
		t.Fatalf("focused bar should show query:\n%s", plainQuery)
	}
	if strings.Contains(plainQuery, "Search…") {
		t.Fatalf("non-empty query should not show placeholder:\n%s", plainQuery)
	}

	// Width 0 skips MaxWidth so style equality is exact.
	focused := RenderSearchBar(theme, "q", "Search…", true, 0)
	unfocused := RenderSearchBar(theme, "q", "Search…", false, 0)
	if focused == unfocused {
		t.Fatalf("focused and unfocused styles should differ")
	}
	if want := theme.ControlActive.Render("/ q"); focused != want {
		t.Fatalf("focused bar should use ControlActive\ngot=%q\nwant=%q", focused, want)
	}
	if want := theme.Muted.Render("/ q"); unfocused != want {
		t.Fatalf("unfocused bar should use Muted\ngot=%q\nwant=%q", unfocused, want)
	}
}

func TestSearchBar_TruncatesToWidth(t *testing.T) {
	theme := DefaultTheme()
	const width = 12
	got := RenderSearchBar(theme, "a-very-long-search-query", "Search…", false, width)
	if w := lipgloss.Width(got); w > width {
		t.Fatalf("width=%d exceeds max %d: %q", w, width, stripANSI(got))
	}
}
