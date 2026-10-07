package subscriptions

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type actionClient struct {
	*fakeClient
	useRequest protocol.MutationRequest
	useID      string
	useResult  protocol.SubscriptionResult
}

func (c *actionClient) UseSubscription(ctx context.Context, id string, request protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	c.captureMutation(ctx, request.OperationID)
	c.useID, c.useRequest = id, request
	return c.useResult, nil
}

func focusDetailAction(t *testing.T, m *Model, label string) {
	t.Helper()
	for range len(m.form.inputs) + 1 {
		if m.form.index < len(m.form.labels) && m.form.labels[m.form.index] == label {
			return
		}
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	t.Fatalf("details lack focusable %s action", label)
}

func TestDetailAction_EnabledPreservesDraftAndAdvancesSaveRevision(t *testing.T) {
	p := protocol.Subscription{ID: "a", Name: "Main", Enabled: true, Cached: true}
	disabled := p
	disabled.Enabled = false
	c := &fakeClient{toggleResult: protocol.SubscriptionResult{Revision: 8, Subscription: disabled}}
	m := New(c, func() string { return "toggle" }, nil)
	m.SetSize(100, 36)
	m.SetSubscriptions(protocol.SubscriptionList{Revision: 7, ActiveID: "a", Subscriptions: []protocol.Subscription{p}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.form.inputs[0].SetValue("Draft")
	focusDetailAction(t, m, "Enabled")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "[ Disable ]") || !strings.Contains(view, "immediately") {
		t.Fatalf("missing explicit immediate action: %s", view)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, again := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); again != nil {
		t.Fatal("duplicate action accepted")
	}
	drainCmd(t, m, cmd)
	if c.toggle.Enabled || c.toggle.IfRevision == nil || *c.toggle.IfRevision != 7 {
		t.Fatalf("toggle request=%+v", c.toggle)
	}
	if m.form == nil || m.form.inputs[0].Value() != "Draft" || m.activeID != "" || m.subscriptions[0].Enabled || m.formRevision != 8 {
		t.Fatal("action lost draft, selection, enabled state, or save revision")
	}
	if !strings.Contains(ansi.Strip(m.View()), "[ Enable ]") {
		t.Fatal("action did not reflect new enabled state")
	}
	m.form.index = len(m.form.inputs)
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainCmd(t, m, cmd)
	if c.update.IfRevision == nil || *c.update.IfRevision != 8 || c.update.Name == nil || *c.update.Name != "Draft" {
		t.Fatal("subsequent Save lost the draft or used the pre-action revision")
	}
}

func TestDetailAction_InUseUsesExistingMutation(t *testing.T) {
	p := protocol.Subscription{ID: "a", Name: "Main", Enabled: true, Cached: true}
	c := &actionClient{fakeClient: &fakeClient{}, useResult: protocol.SubscriptionResult{Revision: 8, Subscription: p}}
	m := New(c, func() string { return "use" }, nil)
	m.SetSize(100, 36)
	m.SetSubscriptions(protocol.SubscriptionList{Revision: 7, ActiveID: "other", Subscriptions: []protocol.Subscription{p}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	focusDetailAction(t, m, "InUse")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainCmd(t, m, cmd)
	if c.useID != "a" || c.useRequest.IfRevision == nil || *c.useRequest.IfRevision != 7 || m.activeID != "a" || m.form == nil {
		t.Fatal("detail action did not activate selected subscription and keep details open")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("already active subscription submitted another use")
	}
}

func TestDetailAction_InUseUnavailableWithoutEnabledCache(t *testing.T) {
	for _, tc := range []struct {
		name            string
		enabled, cached bool
		label           string
	}{
		{"disabled", false, true, "Enable first"},
		{"uncached", true, false, "Refresh first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := protocol.Subscription{ID: "a", Name: "Main", Enabled: tc.enabled, Cached: tc.cached}
			c := &fakeClient{}
			m := New(c, nil, nil)
			m.SetSize(100, 36)
			m.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{p}})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			focusDetailAction(t, m, "InUse")
			if !strings.Contains(ansi.Strip(m.View()), tc.label) {
				t.Fatal("missing unavailable reason")
			}
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			drainCmd(t, m, cmd)
			if len(c.mutations) != 0 {
				t.Fatal("unavailable action sent a mutation")
			}
		})
	}
}

func TestDetailAction_ErrorsKeepDraftAndDoNotReplay(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		unknown bool
	}{
		{"rejected", protocol.APIError{Code: protocol.CodeInvalidState, Message: "fixture rejection"}, false},
		{"conflict", protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "fixture conflict"}, false},
		{"unknown", context.DeadlineExceeded, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := protocol.Subscription{ID: "a", Name: "Main", Enabled: true, Cached: true}
			list := protocol.SubscriptionList{Revision: 7, ActiveID: "a", Subscriptions: []protocol.Subscription{p}}
			c := &fakeClient{toggleErr: tc.err, list: list}
			m := New(c, nil, nil)
			m.SetSize(100, 36)
			m.SetSubscriptions(list)
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m.form.inputs[0].SetValue("Draft")
			focusDetailAction(t, m, "Enabled")
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			next := drainCmd(t, m, cmd)
			drainCmd(t, m, next)
			if m.form == nil || m.form.inputs[0].Value() != "Draft" || !m.subscriptions[0].Enabled || m.activeID != "a" || m.form.errorText == "" || len(c.mutations) != 1 {
				t.Fatal("failed action lost draft/state/feedback or replayed")
			}
			if tc.unknown {
				if !strings.Contains(ansi.Strip(m.View()), "outcome unknown") {
					t.Fatal("unknown outcome not visible")
				}
				_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				drainCmd(t, m, cmd)
				if len(c.mutations) != 1 {
					t.Fatal("unknown action replayed")
				}
			}
		})
	}
}

func TestDetailAction_LateResultPreservesNewDialogAndCatalog(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"success", nil},
		{"rejected", protocol.APIError{Code: protocol.CodeInvalidState, Message: "old rejection"}},
		{"conflict", protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "old conflict"}},
		{"unknown", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := protocol.Subscription{ID: "a", Name: "Main", Enabled: true, Cached: true}
			c := &fakeClient{toggleErr: tc.err, toggleResult: protocol.SubscriptionResult{Revision: 8, Subscription: protocol.Subscription{ID: "a", Name: "Main", Enabled: false}}}
			m := New(c, nil, nil)
			m.SetSubscriptions(protocol.SubscriptionList{Revision: 7, ActiveID: "a", Subscriptions: []protocol.Subscription{p}})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			focusDetailAction(t, m, "Enabled")
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			p.Name = "Newer"
			m.SetSubscriptions(protocol.SubscriptionList{Revision: 9, ActiveID: "a", Subscriptions: []protocol.Subscription{p}})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m.form.inputs[0].SetValue("New draft")
			m.lastError = "New page error"
			m.form.errorText = "New form error"
			c.list = protocol.SubscriptionList{Revision: 9, ActiveID: "a", Subscriptions: []protocol.Subscription{p}}
			next := drainCmd(t, m, cmd)
			drainCmd(t, m, next)
			if m.form == nil || m.form.inputs[0].Value() != "New draft" || m.revision != 9 || m.subscriptions[0].Name != "Newer" || !m.subscriptions[0].Enabled || m.activeID != "a" {
				t.Fatal("late action changed newer dialog or catalog")
			}
			if m.lastError != "New page error" || m.form.errorText != "New form error" {
				t.Fatal("late action changed current error feedback")
			}
		})
	}
}

func TestDetailAction_ExternalChangesStillConflictOnSave(t *testing.T) {
	p := protocol.Subscription{ID: "a", Name: "Main", Enabled: true, Cached: true}
	c := &fakeClient{toggleResult: protocol.SubscriptionResult{Revision: 9, Subscription: p}}
	m := New(c, nil, nil)
	m.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{p}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.form.inputs[0].SetValue("Draft")
	p.Name = "External edit"
	m.SetSubscriptions(protocol.SubscriptionList{Revision: 8, Subscriptions: []protocol.Subscription{p}})
	focusDetailAction(t, m, "Enabled")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainCmd(t, m, cmd)
	if m.formRevision != 7 || m.form.inputs[0].Value() != "Draft" || *c.toggle.IfRevision != 8 {
		t.Fatal("immediate action silently accepted external changes for the draft")
	}
}

func TestDetailAction_NarrowLayoutAndHelp(t *testing.T) {
	for _, width := range []int{40, 54, 100} {
		m := compactDetail()
		m.activeID = ""
		m.SetSize(width, 20)
		for _, label := range []string{"Enabled", "InUse"} {
			focusDetailAction(t, m, label)
			view := ansi.Strip(m.View())
			if !strings.Contains(view, label) || !strings.Contains(view, "[ Save ]") {
				t.Fatalf("action or Save hidden: %s", view)
			}
			if label == "InUse" && !strings.Contains(view, "[ Use this subscription ]") {
				t.Fatalf("action label clipped: %s", view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("action overflows %d cells: %q", width, line)
				}
			}
			if len(strings.Split(view, "\n")) > 20 {
				t.Fatal("dialog exceeds window height")
			}
			if m.formHelpMode() != ui.ModeSubscriptionAction || !strings.Contains(m.formFooter(), "Enter apply now") {
				t.Fatal("missing action help")
			}
		}
	}
}

func TestDetailAction_UnknownFeedbackVisibleInShortWindow(t *testing.T) {
	p := protocol.Subscription{ID: "a", Name: "Main", Enabled: true, Cached: true}
	c := &fakeClient{toggleErr: context.DeadlineExceeded}
	m := New(c, nil, nil)
	m.SetSize(54, 20)
	m.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{p}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	focusDetailAction(t, m, "Enabled")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainCmd(t, m, cmd)
	if !strings.Contains(ansi.Strip(m.View()), "Action outcome unknown.") {
		t.Fatalf("uncertain action feedback hidden: %s", ansi.Strip(m.View()))
	}
}
