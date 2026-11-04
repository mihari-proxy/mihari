package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

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
				status := strings.Index(line, "No changes")
				right := status + strings.Index(line[status:], "│")
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
