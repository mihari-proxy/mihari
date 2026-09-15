package proxies

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// TestSelectedNodeMarker_UsesLogInfoCircle keeps selection color aligned with INFO logs while preserving focus, pending state, and card geometry.
func TestSelectedNodeMarker_UsesLogInfoCircle(t *testing.T) {
	for _, tc := range []struct {
		name           string
		selected       bool
		focused        bool
		contentFocused bool
		pending        bool
	}{
		{name: "selected", selected: true},
		{name: "selected and focused", selected: true, focused: true, contentFocused: true},
		{name: "selected while rail focused", selected: true, focused: true},
		{name: "unselected and focused", focused: true, contentFocused: true},
		{name: "selected and pending", selected: true, pending: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(nil, nil)
			node := protocol.ProxyNode{Name: "node-a", Type: "VLESS"}
			group := protocol.ProxyGroup{Name: "GLOBAL"}
			if tc.selected {
				group.Now = node.Name
			}
			id := FocusID{Group: group.Name, Node: node.Name}
			if tc.focused {
				m.focus = id
			}
			m.SetContentFocused(tc.contentFocused)
			m.pending[id] = tc.pending
			view := m.renderNode(group, node, 28)
			plain := ansi.Strip(view)
			if strings.ContainsAny(plain, "✓√") {
				t.Fatal("selected card retained the checkmark")
			}
			if tc.selected && !tc.pending {
				// Compare with the log-level renderer to keep both surfaces in sync.
				marker := strings.Replace(ui.StyleLogLevel(m.theme, "INFO"), "INFO", "●", 1)
				if !strings.Contains(view, marker+" "+node.Name) {
					t.Fatal("selected circle does not use INFO styling independently of the node name")
				}
			} else if strings.Contains(plain, "●") {
				t.Fatal("an unselected or pending card displayed the selected circle")
			}
			if tc.pending && !strings.Contains(plain, "… "+node.Name) {
				t.Fatal("pending selection lost its progress marker")
			}
			group.Now = ""
			unselected := m.renderNode(group, node, 28)
			if lipgloss.Width(view) != lipgloss.Width(unselected) || lipgloss.Height(view) != lipgloss.Height(unselected) {
				t.Fatal("selected marker changed the card dimensions")
			}
		})
	}
}
