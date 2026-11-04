package tui

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type renderCountingPage struct {
	loadCountingPage
	views int
}

func (p *renderCountingPage) View() string { p.views++; return "underlying page" }

func TestModel_OverlaySkipsUnderlyingPageRender(t *testing.T) {
	for _, setup := range []bool{false, true} {
		for _, export := range []bool{false, true} {
			model := NewModel()
			model.width, model.height = 120, 40
			if setup {
				model.active = ui.PageSetup
			}
			page := &renderCountingPage{}
			model.pages[model.active] = page
			model.modal = NewDetail("modal title", "modal body")
			want := "modal body"
			if export {
				model.exportLogs = ui.NewExportLogsModel(ui.ExportLogsOptions{})
				model.exportLogs.Open()
				want = ui.ExportLogsTitle
			}
			view := model.View()
			if page.views != 0 || !strings.Contains(view.Content, want) || !view.AltScreen || view.WindowTitle != ui.AppName {
				t.Fatalf("setup=%v export=%v views=%d content=%q", setup, export, page.views, view.Content)
			}
		}
	}
}
