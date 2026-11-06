package subscriptions

import (
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"testing"
	"time"
)

func TestSharedRefresh_OldSubscriptionCannotStartCandidateWrites(t *testing.T) {
	m := New(&fakeClient{}, func() string { return "op" }, time.Now)
	defer m.Stop()
	m.SetSnapshotRefresh(func(bool) tea.Cmd { return nil })
	sub := protocol.Subscription{ID: "fixture", Name: "fixture", Enabled: true, Cached: true}
	m.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{sub}})
	m.SetSnapshotState("subscriptions", ui.SnapshotState{Loaded: true, Pending: true})
	for name, cmd := range map[string]tea.Cmd{
		"toggle": m.toggle(sub), "proxy": m.cycleProxy(sub), "refresh": m.refresh(sub.ID),
		"all": m.refreshAllWithID("op"), "use": m.use(sub.ID), "remove": m.remove(sub.ID, "op", 1),
	} {
		if cmd != nil {
			t.Fatalf("%s accepted stale data", name)
		}
	}
	m.SetSnapshotState("subscriptions", ui.SnapshotState{Loaded: true})
	if m.toggle(sub) == nil {
		t.Fatal("fresh subscription remained disabled")
	}
}

func TestSharedRefresh_DeleteGateRunsBeforeCommand(t *testing.T) {
	m := New(&fakeClient{}, func() string { return "op" }, time.Now)
	defer m.Stop()
	m.SetSnapshotRefresh(func(bool) tea.Cmd { return nil })
	m.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "fixture", Name: "fixture"}}})
	m.focus = pageFocus{kind: focusRow, id: "fixture"}
	m.SetSnapshotState("subscriptions", ui.SnapshotState{Loaded: true, Pending: true})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmd != nil || m.lastError == "" {
		t.Fatal("delete gate deferred to command goroutine")
	}
	m.SetSnapshotState("subscriptions", ui.SnapshotState{Loaded: true})
	if m.lastError != "" {
		t.Fatal("successful refresh retained stale gate warning")
	}
}

func TestSharedRefresh_BulkRefreshGateDoesNotOpenConfirmation(t *testing.T) {
	m := New(&fakeClient{}, func() string { return "op" }, time.Now)
	defer m.Stop()
	m.SetSnapshotRefresh(func(bool) tea.Cmd { return nil })
	m.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "fixture"}}})
	m.SetSnapshotState("subscriptions", ui.SnapshotState{Loaded: true, Pending: true})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("blocked refresh opened empty execution confirmation")
	}
}
