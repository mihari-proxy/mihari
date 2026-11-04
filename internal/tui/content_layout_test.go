package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type centeredDialogPage struct{ width, height int }

func (p *centeredDialogPage) ID() ui.PageID                     { return ui.PageSystem }
func (p *centeredDialogPage) SetSize(width, height int)         { p.width, p.height = width, height }
func (p *centeredDialogPage) FocusFirst()                       {}
func (p *centeredDialogPage) Update(tea.Msg) (ui.Page, tea.Cmd) { return p, nil }
func (p *centeredDialogPage) View() string {
	theme := ui.DefaultTheme()
	dialog := theme.Dialog.Width(min(144, p.width-4)).Render("Proxy Authentication")
	return ui.CenterOverlay(theme, "", dialog, p.width, p.height)
}

func TestContentLayout_ProxyAuthDialogHasRoomOnBothSides(t *testing.T) {
	for _, width := range []int{72, 90, 100, 101} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			model := NewModel()
			model.width, model.height = width, 50
			model.active = ui.PageSystem
			page := systempage.New(&client.Client{}, nil)
			page.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityProxyAuth}}, protocol.CoreStatus{})
			page.SetMutationsEnabled(true)
			model.pages[ui.PageSystem] = page
			model.resizePages()
			// Navigate to the first row, then past the three ports to authentication.
			// Commands are deliberately not executed: rendering needs no daemon IO.
			for range 100 {
				page.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			}
			for range 3 {
				page.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			page.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if page.HelpMode() != "proxy-auth" {
				t.Fatal("authentication dialog did not open")
			}
			lines := strings.Split(ansi.Strip(model.View().Content), "\n")
			for _, line := range lines {
				if !strings.Contains(line, "No changes") {
					continue
				}
				left := strings.LastIndex(line[:strings.Index(line, "Proxy Authentication")], "│")
				right := strings.LastIndex(line, "│")
				leftMargin := ansi.StringWidth(line[:left]) - calculateLayout(width, model.height).RailWidth
				rightMargin := width - ansi.StringWidth(line[:right+len("│")])
				if leftMargin != 3 || rightMargin != 3 {
					t.Fatalf("authentication dialog margins: left=%d right=%d", leftMargin, rightMargin)
				}
				return
			}
			t.Fatal("authentication header missing")
		})
	}
}

func TestContentLayout_CenteredDialogRetainsSymmetricMargins(t *testing.T) {
	for _, width := range []int{72, 90, 100, 101, 180} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			model := NewModel()
			model.width, model.height = width, 40
			model.active = ui.PageSystem
			page := &centeredDialogPage{}
			model.pages[ui.PageSystem] = page
			model.resizePages()
			layout := calculateLayout(width, model.height)
			view := model.View().Content
			if lipgloss.Width(view) > width || lipgloss.Height(view) > model.height {
				t.Fatalf("shell exceeds viewport: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
			}
			for _, line := range strings.Split(ansi.Strip(view), "\n") {
				left := strings.Index(line, "╭")
				if left < 0 {
					continue
				}
				right := strings.Index(line, "╮")
				if right < 0 {
					t.Fatal("right dialog corner clipped")
				}
				leftMargin := ansi.StringWidth(line[:left]) - layout.RailWidth
				rightMargin := width - ansi.StringWidth(line[:right+len("╮")])
				if leftMargin < 1 || rightMargin < leftMargin || rightMargin-leftMargin > 1 {
					t.Fatalf("dialog margins differ: left=%d right=%d", leftMargin, rightMargin)
				}
				return
			}
			t.Fatal("dialog top border missing")
		})
	}
}
