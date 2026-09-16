package ui

import (
	"strings"
	"testing"
)

func TestDiagnosticKey_DiscoverableInEveryPageAndInputMode(t *testing.T) {
	for _, page := range []PageID{PageSetup, PageOverview, PageProxies, PageSubscriptions, PageConnections, PageRules, PageLogs, PageWebGUI, PageSystem} {
		for _, mode := range []string{"", ModeSearch, ModeForm, ModeSubscriptionInput, ModeSubscriptionSaving, ModePortsEdit, ModeLoggingEdit, ModeExportLogs, ModeConfirm, ModeDetail, ModePanelMenu} {
			if !strings.Contains(RenderHelp(page, mode), "F2") || !strings.Contains(RenderFooter(page, mode, FooterOpt{}), "F2") {
				t.Fatalf("F2 missing on %s / %s", page, mode)
			}
		}
	}
	if !strings.Contains(RenderRailFooter(), "F2") {
		t.Fatal("rail missing F2")
	}
}
