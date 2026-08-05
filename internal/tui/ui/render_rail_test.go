package ui

import (
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

	// Both should mark the selected page.
	if !strings.Contains(stripANSI(railFocused), "› Subs") || !strings.Contains(stripANSI(contentFocused), "› Subs") {
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
