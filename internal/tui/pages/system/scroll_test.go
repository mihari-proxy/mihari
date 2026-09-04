package system

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func aboutFixture(t *testing.T) *Model {
	t.Helper()
	model := New(&fakeClient{}, func() string { return "op" })
	model.SetSnapshot(protocol.Status{}, protocol.CoreStatus{})
	model.SetSize(80, 40)
	model.SetContentFocused(true)
	full := strings.Count(model.View(), "\n") + 1
	if full <= 12 {
		t.Fatalf("about fixture view lines=%d want > 12", full)
	}
	return model
}

func viewLineCount(view string) int {
	if view == "" {
		return 0
	}
	return strings.Count(view, "\n") + 1
}

func downToGitHub(t *testing.T, model *Model) *Model {
	t.Helper()
	limit := len(model.rows()) + 2
	for i := 0; i < limit && model.focusID != rowGitHub; i++ {
		model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if model.focusID != rowGitHub {
		t.Fatalf("focus=%q want %q after %d downs", model.focusID, rowGitHub, limit)
	}
	return model
}

func TestView_ShortHeightKeepsFocusedRowVisible(t *testing.T) {
	model := aboutFixture(t)
	model.SetSize(80, 12)
	model.FocusFirst()
	model.SetContentFocused(true)
	if model.focusID != rowMixed {
		t.Fatalf("focus=%q want %q", model.focusID, rowMixed)
	}
	top := model.View()
	if strings.Contains(top, ui.AboutSectionTitle) || strings.Contains(top, ui.AboutGitHubDisplay) {
		t.Fatalf("short top view still shows About:\n%s", top)
	}
	downs := 0
	limit := len(model.rows()) + 2
	for model.focusID != rowGitHub && downs < limit {
		model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyDown})
		downs++
	}
	if model.focusID != rowGitHub {
		t.Fatalf("focus=%q want GitHub", model.focusID)
	}
	view := model.View()
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("focused About missing:\n%s", view)
	}
	if !strings.Contains(view, ui.FocusMarker) {
		t.Fatalf("missing focus marker:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
	if strings.Contains(view, ui.PortsConfigSectionTitle) {
		t.Fatalf("Ports Config still visible after scroll:\n%s", view)
	}
	for i := 0; i < downs; i++ {
		model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if model.focusID != rowMixed {
		t.Fatalf("focus after ups=%q want %q", model.focusID, rowMixed)
	}
	top = model.View()
	if !strings.Contains(top, ui.PortsConfigSectionTitle) {
		t.Fatalf("Ports Config missing after scroll up:\n%s", top)
	}
	if strings.Contains(top, ui.AboutSectionTitle) {
		t.Fatalf("About still visible at top:\n%s", top)
	}
	if model.scrollY != 0 {
		t.Fatalf("scrollY=%d want 0 at top", model.scrollY)
	}
}

func TestView_ShortHeightKeepsFocusedLoggingRowVisible(t *testing.T) {
	model := aboutFixture(t)
	model.ApplyLoggingSync(ui.LoggingSyncMsg{Epoch: 2, Available: true, Status: protocol.LoggingStatus{
		Revision: 3, Level: "warn", MaxSizeMB: 25, MaxFiles: 6, Dir: `C:\logs`,
	}})
	model.SetLocalLoggingAvailable(false)
	model.SetSize(80, 11)
	model.focusID = rowLogDirectory
	model.ensureFocusVisible()

	view := model.View()
	if !strings.Contains(view, ui.LoggingSectionTitle) || !strings.Contains(view, ui.LoggingDirectoryLabel) {
		t.Fatalf("focused Logging row not visible:\n%s", view)
	}
	if !strings.Contains(view, ui.LocalFileLogUnavailable) {
		t.Fatalf("local writer health marker not visible:\n%s", view)
	}
	if viewLineCount(view) > 11 {
		t.Fatalf("view lines=%d exceed 11\n%s", viewLineCount(view), view)
	}
}

func TestView_ErrorDetailPinnedWhileScrolled(t *testing.T) {
	model := aboutFixture(t)
	model.SetSize(80, 12)
	model.FocusFirst()
	model.SetContentFocused(true)
	model = downToGitHub(t, model)
	if strings.Contains(model.View(), ui.PortsConfigSectionTitle) {
		t.Fatalf("expected Ports Config scrolled away before error:\n%s", model.View())
	}
	model.SetOpenBrowser(func(string) error { return errors.New("browser missing") })
	model = updateKey(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	view := model.View()
	first := strings.Split(view, "\n")[0]
	if !strings.Contains(first, ui.AboutGitHubOpenFailed) {
		t.Fatalf("error not pinned on first line %q\n%s", first, view)
	}
	if strings.Contains(view, "browser missing") {
		t.Fatalf("raw browser error leaked:\n%s", view)
	}
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("About missing after error chrome:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
	if strings.Contains(view, ui.PortsConfigSectionTitle) {
		t.Fatalf("Ports Config visible after error:\n%s", view)
	}
}

func TestFocusFirst_PreservesGitHubAndScrolls(t *testing.T) {
	model := aboutFixture(t)
	model.SetSize(80, 12)
	model.SetContentFocused(true)
	model = downToGitHub(t, model)
	model.FocusFirst()
	if model.focusID != rowGitHub {
		t.Fatalf("FocusFirst reset focus to %q", model.focusID)
	}
	view := model.View()
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("GitHub not visible after FocusFirst:\n%s", view)
	}
	if strings.Contains(view, ui.PortsConfigSectionTitle) {
		t.Fatalf("Ports Config visible after FocusFirst:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
}

func TestView_SetSnapshotKeepsGitHubVisible(t *testing.T) {
	model := aboutFixture(t)
	model.SetSize(80, 12)
	model.SetContentFocused(true)
	model = downToGitHub(t, model)
	model.SetSnapshot(protocol.Status{
		Capabilities: []string{protocol.CapabilitySystemProxy, protocol.CapabilityTUN},
	}, protocol.CoreStatus{})
	if model.focusID != rowGitHub {
		t.Fatalf("focus=%q after SetSnapshot", model.focusID)
	}
	view := model.View()
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("GitHub missing after SetSnapshot:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
}

func TestView_LongErrorChromeStaysOneLine(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		snippet string
		extra   string
	}{
		{
			name:    "elevation",
			payload: ui.ServiceElevationRequired,
			snippet: "Administrator",
		},
		{
			name:    "multiline",
			payload: "line-one\n" + strings.Repeat("x", 80),
			snippet: "line-one",
			extra:   "xxx",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := aboutFixture(t)
			model.SetSize(58, 12)
			model.SetContentFocused(true)
			model = downToGitHub(t, model)
			model.lastError = tc.payload
			model.ensureFocusVisible()
			chrome := model.errorChromeLines()
			if len(chrome) != 1 {
				t.Fatalf("chrome lines=%d", len(chrome))
			}
			if got := lipgloss.Width(chrome[0]); got > 56 {
				t.Fatalf("chrome visual width=%d want <=56", got)
			}
			view := model.View()
			first := strings.Split(view, "\n")[0]
			if !strings.Contains(first, tc.snippet) {
				t.Fatalf("first line %q missing %q\n%s", first, tc.snippet, view)
			}
			if tc.extra != "" && !strings.Contains(first, tc.extra) {
				t.Fatalf("first line %q missing %q (newline must become space)\n%s", first, tc.extra, view)
			}
			if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
				t.Fatalf("GitHub/About missing:\n%s", view)
			}
			if viewLineCount(view) > 12 {
				t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
			}
		})
	}
}

func TestView_ApplyServiceStatusKeepsGitHubVisible(t *testing.T) {
	withElevation(t, true)
	svc := &fakeService{status: service.StatusNotInstalled}
	model := NewWithService(&fakeClient{}, svc, func() string { return "system-op" })
	updated, _ := model.Update(serviceStatusMsg{status: service.StatusNotInstalled, elevated: true})
	model = updated.(*Model)
	model.SetSnapshot(protocol.Status{}, protocol.CoreStatus{})
	model.SetSize(80, 40)
	model.SetContentFocused(true)
	full := strings.Count(model.View(), "\n") + 1
	if full <= 12 {
		t.Fatalf("service fixture view lines=%d want > 12", full)
	}
	model.SetSize(80, 12)
	model = downToGitHub(t, model)
	beforeRows := len(model.rows())
	model.ApplyServiceStatus(service.StatusRunning, true)
	if got := len(model.rows()); got <= beforeRows {
		t.Fatalf("ApplyServiceStatus did not grow rows: %d → %d", beforeRows, got)
	}
	if model.focusID != rowGitHub {
		t.Fatalf("focus=%q after ApplyServiceStatus", model.focusID)
	}
	view := model.View()
	if !strings.Contains(view, ui.AboutSectionTitle) || !strings.Contains(view, ui.AboutGitHubLabel) {
		t.Fatalf("GitHub missing after ApplyServiceStatus:\n%s", view)
	}
	if viewLineCount(view) > 12 {
		t.Fatalf("view lines=%d exceed 12\n%s", viewLineCount(view), view)
	}
}
