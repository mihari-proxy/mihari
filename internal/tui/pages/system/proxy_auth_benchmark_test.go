package system

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// Diagnostic benchmarks use synthetic data only and never execute IO commands.
func authLatencyFixture(users, previewLines, width, height int) *Model {
	m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
	m.SetSize(width, height)
	m.mihariChannelLoaded = true
	m.mihariChannel = "dev"
	m.auth = authUI{open: true, loaded: true, epoch: 1, sourceLoaded: true, sourceID: "fixture", sourceName: "Synthetic subscription", draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom"}, preview: protocol.ProxyAuthPreview{ActiveID: "fixture"}}
	for i := range users {
		u := protocol.ProxyAuthUser{Username: fmt.Sprintf("synthetic-%04d", i), Source: "subscription"}
		m.auth.sourceUsers = append(m.auth.sourceUsers, u)
		m.auth.draft.Users = append(m.auth.draft.Users, protocol.ProxyAuthUser{Username: u.Username, Password: "synthetic-only"})
	}
	for i := range previewLines {
		m.auth.preview.Lines = append(m.auth.preview.Lines, protocol.ProxyAuthPreviewLine{Text: "  - synthetic-user:********", Field: "authentication", Target: fmt.Sprintf("synthetic-%04d", i%max(1, users))})
	}
	m.authEdit("user", "")
	m.auth.inputs[0].SetValue("new-synthetic-user")
	m.auth.input = 1
	m.auth.inputs[0].Blur()
	m.auth.inputs[1].Focus()
	return m
}

func BenchmarkAuthLatencyInvestigation(b *testing.B) {
	for _, spec := range []struct {
		name                        string
		users, lines, width, height int
	}{
		{"small", 0, 2, 160, 50},
		{"small_large_terminal", 0, 2, 240, 70},
		{"ten_users", 10, 12, 160, 50},
		{"hundred_users", 100, 102, 160, 50},
		{"large_preview", 0, 1000, 160, 50},
	} {
		for _, path := range []string{"controls", "update", "dialog", "page", "update_dialog", "update_page", "dispatch", "dispatch_page"} {
			b.Run(spec.name+"/"+path, func(b *testing.B) {
				m := authLatencyFixture(spec.users, spec.lines, spec.width, spec.height)
				key := tea.KeyPressMsg{Code: 'x', Text: "x"}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					switch path {
					case "controls":
						m.auth.inputs[1].SetValue("synthetic")
						m.updateAuthControls(key)
					case "update":
						m.auth.inputs[1].SetValue("synthetic")
						m.updateAuth(key)
					case "dialog":
						m.authView()
					case "page":
						m.View()
					case "update_dialog":
						m.auth.inputs[1].SetValue("synthetic")
						m.updateAuth(key)
						m.authView()
					case "update_page":
						m.auth.inputs[1].SetValue("synthetic")
						m.updateAuth(key)
						m.View()
					case "dispatch":
						m.auth.inputs[1].SetValue("synthetic")
						m.Update(key)
					case "dispatch_page":
						m.auth.inputs[1].SetValue("synthetic")
						m.Update(key)
						m.View()
					}
				}
			})
		}
	}
}
