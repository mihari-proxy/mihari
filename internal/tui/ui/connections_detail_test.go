package ui

import (
	"strings"
	"testing"
)

// TestConnectionsDetailHelp_NoTabBinding guards the single-page help contract.
func TestConnectionsDetailHelp_NoTabBinding(t *testing.T) {
	help := RenderHelp(PageConnections, ModeDetail)
	if strings.Contains(help, "switch tabs") {
		t.Fatal("connections details still advertise tabs")
	}
}

// TestConnectionsDetailFooter_ScrollAndReturn checks the available detail actions.
func TestConnectionsDetailFooter_ScrollAndReturn(t *testing.T) {
	footer := RenderFooter(PageConnections, ModeDetail, FooterOpt{})
	for _, want := range []string{"↑/↓ scroll", "Enter/Esc close"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer missing %q: %s", want, footer)
		}
	}
}

// TestLogsDetailFooter_NoConnectionScroll keeps page-specific hints isolated.
func TestLogsDetailFooter_NoConnectionScroll(t *testing.T) {
	if strings.Contains(RenderFooter(PageLogs, ModeDetail, FooterOpt{}), "scroll") {
		t.Fatal("connection scroll hint leaked into logs")
	}
}
