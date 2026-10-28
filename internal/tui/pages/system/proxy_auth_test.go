package system

import (
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"strings"
	"testing"
)

func TestProxyAuth_EntrySharesPortsSection(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityProxyAuth}}, protocol.CoreStatus{})
	ports := m.portRows()
	section := ""
	authSection := ""
	for _, r := range ports {
		if r.id == rowMixed {
			section = r.section
		}
		if r.id == "proxy-auth" {
			authSection = r.section
		}
	}
	if authSection == "" || authSection != section || section != "Ports & Authentication" {
		t.Fatalf("auth=%q ports=%q", authSection, section)
	}
}

func TestProxyAuth_DraftRemovalAndScrollDoNotApply(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(72, 22)
	m.auth = authUI{open: true, loaded: true, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom"}}
	for i := range 25 {
		u := protocol.ProxyAuthUser{Username: fmt.Sprintf("fixture-%02d", i), Source: "added"}
		m.auth.status.Users = append(m.auth.status.Users, u)
		m.auth.draft.Users = append(m.auth.draft.Users, u)
	}
	m.auth.selected = 3
	m.updateAuth(tea.KeyPressMsg{Code: 'd'})
	if len(m.authUsers()) != 24 {
		t.Fatal("removed instance user remains visible")
	}
	m.auth.selected = len(m.authRows()) - 1
	view := m.authView()
	if !strings.Contains(view, "[ Save ]") || m.auth.top == 0 {
		t.Fatal("actions did not scroll into view")
	}
	if lipgloss.Width(view) > 72 || lipgloss.Height(view) > 22 {
		t.Fatalf("auth dialog exceeds viewport: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
	}
	if m.auth.pending {
		t.Fatal("draft interaction applied config")
	}
}
