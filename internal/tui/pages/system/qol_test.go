package system

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestRows_NetworkBetweenPortsAndDaemon(t *testing.T) {
	m := New(nil, nil)
	rows := m.rows()
	var sections []string
	for _, r := range rows {
		if len(sections) == 0 || sections[len(sections)-1] != r.section {
			sections = append(sections, r.section)
		}
	}
	if len(sections) < 3 || sections[0] != ui.PortsConfigSectionTitle || sections[1] != ui.NetworkSectionTitle || sections[2] != ui.DaemonSectionTitle {
		t.Fatalf("section order=%v", sections)
	}
	for i, r := range rows {
		if r.section == ui.NetworkSectionTitle && rows[i+1].section == ui.DaemonSectionTitle {
			m.SetSize(80, 10)
			m.focusID = r.id
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			if m.focusID != rowDaemon || !strings.Contains(m.View(), ui.DaemonLabel) {
				t.Fatalf("focus did not follow Network into visible Daemon: %s", m.View())
			}
			return
		}
	}
	t.Fatal("missing Network/Daemon navigation boundary")
}
