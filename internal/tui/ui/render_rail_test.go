package ui

import (
	"strconv"
	"strings"
	"testing"
)

func TestRenderRail_UsesUnderlineWhenContentOwnsFocus(t *testing.T) {
	theme := DefaultTheme()
	pages := []PageID{PageOverview, PageSubscriptions}

	railFocused := RenderRail(theme, pages, 1, true, 24, 10)
	contentFocused := RenderRail(theme, pages, 1, false, 24, 10)

	// Brand is status-bar only; rail must not repeat AppName.
	if strings.Contains(stripANSI(railFocused), AppName) {
		t.Fatalf("rail should not repeat brand name: %q", railFocused)
	}

	// Both should mark the selected page. The 1-based rail shortcut digit now
	// prefixes the label, so Subscriptions at index 1 reads "› 2 Subs".
	if !strings.Contains(stripANSI(railFocused), "› 2 Subs") || !strings.Contains(stripANSI(contentFocused), "› 2 Subs") {
		t.Fatalf("selected page missing\nrail=%q\ncontent=%q", railFocused, contentFocused)
	}
	// Strong accent styling only while the rail itself is focused.
	if !strings.Contains(railFocused, "\x1b[") {
		t.Fatalf("rail-focused selection missing style: %q", railFocused)
	}
	// Content-focused current page uses quieter styling (still ANSI), but the
	// two rendered strings must differ so they are not the same highlight treatment.
	if railFocused == contentFocused {
		t.Fatalf("rail and content focus styles are identical")
	}
	if !strings.Contains(contentFocused, "\x1b[") {
		t.Fatalf("content-focused current page missing style: %q", contentFocused)
	}
}

func TestRenderRailShowsDigitShortcuts(t *testing.T) {
	rendered := stripANSI(RenderRail(DefaultTheme(), RailPages(), 0, false, 24, 10))
	for index, page := range RailPages() {
		want := strconv.Itoa(index+1) + " " + PageLabel(page)
		if !strings.Contains(rendered, want) {
			t.Fatalf("rail missing shortcut label %q: %s", want, rendered)
		}
	}
}

func TestRenderRail_DoesNotWrapOverviewAtCompactWidth(t *testing.T) {
	// Compact layout uses a 14-column rail. "1 Overview" is the longest
	// unselected label; word-wrap at the space leaves the name on its own line.
	const compactRailWidth = 14
	for _, focused := range []bool{true, false} {
		rendered := stripANSI(RenderRail(DefaultTheme(), RailPages(), 0, focused, compactRailWidth, 12))
		if !railLineHasDigitAndLabel(rendered, "1", "Overview") {
			t.Fatalf("Overview wrapped at compact width %d focused=%v:\n%s", compactRailWidth, focused, rendered)
		}
	}
	// Unselected Overview is the screenshot case: user is on System, rail still
	// must keep "1 Overview" on one line.
	unselected := stripANSI(RenderRail(DefaultTheme(), RailPages(), 7, true, compactRailWidth, 12))
	if !railLineHasDigitAndLabel(unselected, "1", "Overview") {
		t.Fatalf("unselected Overview wrapped at compact width %d:\n%s", compactRailWidth, unselected)
	}
}

func railLineHasDigitAndLabel(rendered, digit, label string) bool {
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, label) && strings.Contains(line, digit) {
			return true
		}
	}
	return false
}

func stripANSI(value string) string {
	var builder strings.Builder
	inEscape := false
	for _, r := range value {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && ((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')):
			inEscape = false
		case !inEscape:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
