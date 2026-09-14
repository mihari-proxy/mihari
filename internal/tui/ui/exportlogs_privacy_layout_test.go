package ui

import (
	"fmt"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestExportPrivacyNotice_VisibleInNarrowAndPlainViews(t *testing.T) {
	for _, width := range []int{48, 72, 120} {
		for _, stage := range []string{"form", "pending", "failed", "complete"} {
			t.Run(fmt.Sprintf("%d/%s", width, stage), func(t *testing.T) {
				m := NewExportLogsModel(ExportLogsOptions{Now: exportTestNow, DefaultDir: "/logs"})
				m.Open()
				switch stage {
				case "pending":
					m.pending = true
				case "failed":
					m.message = "Export failed. Try again."
				case "complete":
					m.resultPath = "/logs/result.zip"
				}
				view := m.View(width, 50)
				if lipgloss.Width(view) > width {
					t.Fatalf("dialog width=%d exceeds %d", lipgloss.Width(view), width)
				}
				plain := ansi.Strip(view)
				plain = strings.ReplaceAll(plain, "│", " ")
				plain = strings.Join(strings.Fields(plain), " ")
				for _, phrase := range []string{"Logs are not redacted.", "passwords", "access tokens", "subscription URLs", "user configuration", "Review them before sharing."} {
					if !strings.Contains(plain, phrase) {
						t.Fatalf("%q missing at %s", phrase, stage)
					}
				}
				if !strings.Contains(view, DefaultTheme().Danger.Render("Logs are not redacted.")) {
					t.Fatal("privacy notice lost the Danger style")
				}
			})
		}
	}
}
