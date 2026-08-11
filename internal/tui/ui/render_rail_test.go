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
