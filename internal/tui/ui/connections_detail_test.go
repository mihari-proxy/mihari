package ui

import (
	"strings"
	"testing"
)

func TestConnectionsDetailHelp(t *testing.T) {
	help := RenderHelp(PageConnections, ModeDetail)
	if strings.Contains(help, "switch tabs") {
		t.Fatal("connections details still advertise tabs")
	}
	footer := RenderFooter(PageConnections, ModeDetail, FooterOpt{})
	for _, want := range []string{"↑/↓ scroll", "Enter/Esc close"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer missing %q: %s", want, footer)
		}
	}
	if strings.Contains(RenderFooter(PageLogs, ModeDetail, FooterOpt{}), "scroll") {
		t.Fatal("connection scroll hint leaked into logs")
	}
}
