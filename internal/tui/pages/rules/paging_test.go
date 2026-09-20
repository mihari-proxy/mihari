package rules

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestModel_PageKeys(t *testing.T) {
	for _, view := range []viewKind{viewRules, viewProviders} {
		t.Run(fmt.Sprint(view), func(t *testing.T) {
			m := New(nil, nil)
			m.view, m.query = view, "keep-"
			for i := range 80 {
				name := fmt.Sprintf("keep-%02d", i)
				if i%2 != 0 {
					name = "hidden"
				}
				m.rules = append(m.rules, protocol.Rule{Type: "DOMAIN", Payload: name})
				m.providers = append(m.providers, protocol.RuleProvider{Name: name})
			}
			m.focus = pageFocus{kind: focusRow}
			for _, step := range []struct {
				key          rune
				height, want int
			}{
				{tea.KeyPgDown, 24, 14}, {tea.KeyPgDown, 24, 28},
				{tea.KeyPgDown, 24, 39}, {tea.KeyPgDown, 24, 39},
				{tea.KeyPgUp, 24, 25}, {tea.KeyPgUp, 18, 17},
				{tea.KeyPgUp, 10, 16}, {tea.KeyPgUp, 40, 0}, {tea.KeyPgUp, 24, 0},
			} {
				m.SetSize(120, step.height)
				_, cmd := m.Update(tea.KeyPressMsg{Code: step.key})
				if cmd != nil || m.focus.kind != focusRow || m.focus.row != step.want {
					t.Fatalf("key=%v focus=%+v want row=%d", step.key, m.focus, step.want)
				}
				name := fmt.Sprintf("keep-%02d", step.want*2)
				if !strings.Contains(m.View(), name) {
					t.Fatal("selected row is outside the rendered window")
				}
				if view == viewProviders {
					if m.focusedProvider != name {
						t.Fatalf("provider=%q want=%q", m.focusedProvider, name)
					}
					m.SetProviders(protocol.RuleProviderList{Providers: m.providers})
					if m.focus.row != step.want {
						t.Fatal("refresh lost the paged selection")
					}
				}
			}
		})
	}
}

func TestModel_PageKeysShortAndEmptyList(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 24)
	m.SetRules(protocol.RuleList{Rules: []protocol.Rule{{Payload: "one"}, {Payload: "two"}}})
	m.focus = pageFocus{kind: focusRow}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.focus.row != 1 {
		t.Fatal("page down did not stop at last row")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.focus.row != 0 {
		t.Fatal("page up did not stop at first row")
	}
	m.rules = nil
	before := m.focus
	for _, key := range []rune{tea.KeyPgUp, tea.KeyPgDown} {
		m.Update(tea.KeyPressMsg{Code: key})
		if m.focus != before {
			t.Fatal("paging an empty list changed focus")
		}
	}
}

func TestModel_PageKeysRespectInputOwner(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 24)
	m.SetRules(protocol.RuleList{Rules: []protocol.Rule{{Payload: "one"}, {Payload: "two"}}})
	m.focus = pageFocus{kind: focusRow}
	for _, tt := range []struct {
		name    string
		prepare func(*Model)
	}{
		{"control", func(m *Model) { m.focus.kind = focusControl }},
		{"search", func(m *Model) { m.focus.kind = focusSearch; m.searching = true }},
		{"detail", func(m *Model) { m.openDetail() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := *m
			tt.prepare(&candidate)
			m := &candidate
			before := m.focus
			for _, key := range []rune{tea.KeyPgDown, tea.KeyPgUp} {
				m.Update(tea.KeyPressMsg{Code: key})
				if m.focus != before {
					t.Fatal("page key moved the underlying list or stole focus")
				}
			}
		})
	}
}
