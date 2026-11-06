package rules

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

func TestSharedProviderRefresh_DisablesOldCandidateAndPreservesResultOwner(t *testing.T) {
	m := New(&fakeClient{}, func() string { return "op" })
	m.SetSnapshotRefresh(func(bool) tea.Cmd { return nil })
	m.SetProviders(protocol.RuleProviderList{Providers: []protocol.RuleProvider{{Name: "fixture"}}})
	m.focus = pageFocus{kind: focusRow, row: 0}
	m.SetSnapshotState("rule-providers", ui.SnapshotState{Loaded: true, Pending: true})
	if m.updateFocusedProvider() != nil {
		t.Fatal("old provider candidate remained actionable")
	}
	m.SetSnapshotState("rule-providers", ui.SnapshotState{Loaded: true})
	cmd := m.updateFocusedProvider()
	if cmd == nil {
		t.Fatal("fresh provider remained disabled")
	}
	if result, ok := cmd().(ui.PageResultMsg); !ok || result.Page != ui.PageRules {
		t.Fatal("late provider result lost its page")
	}
}
func TestSharedProviderRefresh_InitialFailureDiffersFromEmpty(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(100, 30)
	m.SetSnapshotState("rule-providers", ui.SnapshotState{Pending: true})
	if view := strings.Join(m.renderProviders(), "\n"); !strings.Contains(view, "Loading") {
		t.Fatal("initial provider data looked empty")
	}
	m.SetSnapshotState("rule-providers", ui.SnapshotState{Failure: "fixture failure"})
	if view := strings.Join(m.renderProviders(), "\n"); !strings.Contains(view, "Unavailable") {
		t.Fatal("failed provider data looked empty")
	}
	m.SetSnapshotState("rule-providers", ui.SnapshotState{Loaded: true})
	if view := strings.Join(m.renderProviders(), "\n"); strings.Contains(view, "Loading") || strings.Contains(view, "Unavailable") {
		t.Fatal("confirmed empty provider data did not settle")
	}
}
