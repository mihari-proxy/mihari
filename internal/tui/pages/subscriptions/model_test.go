package subscriptions

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestRows_DoNotExposeInternalGenerationOrURL(t *testing.T) {
	row := rowFrom(protocol.Subscription{ID: "one", Name: "Main", Generation: 42, Cached: true, LastError: "https://example.test/?token=secret"}, false, false, time.Unix(100, 0), "12h")
	rendered := row.Render()
	for _, forbidden := range []string{"42", "example.test", "token", "secret", "http"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("row leaked %q: %s", forbidden, rendered)
		}
	}
}

func TestRows_RenderActiveCacheAndRefreshStates(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		subscription protocol.Subscription
		active       bool
		updating     bool
		want         []string
	}{
		{"ready", protocol.Subscription{Name: "Ready", Enabled: true, AutoRefresh: true, Cached: true, UpdatedAt: now.Add(-time.Hour)}, true, false, []string{"*", "Ready", "Enabled", "Ready", "1h ago", "in 11h"}},
		{"missing", protocol.Subscription{Name: "Missing", Enabled: true, AutoRefresh: false}, false, false, []string{"Missing", "Manual"}},
		{"stale", protocol.Subscription{Name: "Stale", Enabled: true, AutoRefresh: true, Cached: true, UpdatedAt: now.Add(-13 * time.Hour)}, false, false, []string{"Stale", "Retry pending"}},
		{"updating", protocol.Subscription{Name: "Updating", Enabled: true}, false, true, []string{"Updating"}},
		{"disabled", protocol.Subscription{Name: "Off", Enabled: false, Cached: true}, false, false, []string{"Disabled"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := rowFrom(test.subscription, test.active, test.updating, now, "12h").Render()
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Fatalf("row missing %q: %s", want, got)
				}
			}
		})
	}
}

func TestModel_PinsFocusBySubscriptionIDAcrossReload(t *testing.T) {
	model := New(nil, nil, func() time.Time { return time.Unix(100, 0) })
	model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}})
	model.focus = pageFocus{kind: focusRow, id: "b"}
	model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "b", Name: "B"}, {ID: "a", Name: "A"}}})
	if model.focus.id != "b" {
		t.Fatalf("focus=%#v", model.focus)
	}
}

func TestModel_SelectsNearestNeighborWhenFocusedSubscriptionDisappears(t *testing.T) {
	model := New(nil, nil, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a"}, {ID: "b"}, {ID: "c"}}})
	model.focus = pageFocus{kind: focusRow, id: "b"}
	model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a"}, {ID: "c"}}})
	if model.focus.id != "c" {
		t.Fatalf("focus=%#v", model.focus)
	}
}

func TestModel_MutationUsesRevisionAndReconcilesTypedResult(t *testing.T) {
	client := &fakeClient{toggleResult: protocol.SubscriptionResult{Revision: 8, Subscription: protocol.Subscription{ID: "a", Name: "A", Enabled: false}}}
	model := New(client, func() string { return "toggle-1" }, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A", Enabled: true}}})
	model.focus = pageFocus{kind: focusRow, id: "a"}
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if command == nil || model.pending["a"] == "" {
		t.Fatalf("command=%v pending=%v", command != nil, model.pending)
	}
	model.Update(command())
	if client.toggle.IfRevision == nil || *client.toggle.IfRevision != 7 || client.toggle.OperationID != "toggle-1" || model.revision != 8 || model.subscriptions[0].Enabled {
		t.Fatalf("request=%#v revision=%d subscriptions=%#v", client.toggle, model.revision, model.subscriptions)
	}
}

func TestModel_AddEditRefreshAndUseKeysAreAvailable(t *testing.T) {
	model := New(&fakeClient{}, nil, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 3, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A", Enabled: true, Cached: true}}})
	model.focus = pageFocus{kind: focusRow, id: "a"}
	model.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if model.form == nil || model.form.kind != formAdd {
		t.Fatalf("add form=%#v", model.form)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	model.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if model.form == nil || model.form.kind != formEdit {
		t.Fatalf("edit form=%#v", model.form)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	for _, key := range []tea.KeyPressMsg{{Code: 'r', Text: "r"}, {Code: 'u', Text: "u"}} {
		_, command := model.Update(key)
		if command == nil {
			t.Fatalf("key %q returned no command", key.String())
		}
		model.pending = make(map[string]string)
	}
}

func TestModel_RevisionConflictReloadsAndPreservesFocus(t *testing.T) {
	client := &fakeClient{
		toggleErr: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"},
		list:      protocol.SubscriptionList{Revision: 9, Subscriptions: []protocol.Subscription{{ID: "b", Name: "B"}, {ID: "a", Name: "A", Enabled: false}}},
	}
	model := New(client, nil, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A", Enabled: true}, {ID: "b", Name: "B"}}})
	model.focus = pageFocus{kind: focusRow, id: "a"}
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, reload := model.Update(command())
	if reload == nil {
		t.Fatal("revision conflict did not reload")
	}
	model.Update(reload())
	if model.revision != 9 || model.focus.id != "a" || model.subscriptions[1].ID != "a" {
		t.Fatalf("revision=%d focus=%#v subscriptions=%#v", model.revision, model.focus, model.subscriptions)
	}
}

func TestModel_DeleteRequestsOrdinaryConfirmation(t *testing.T) {
	client := &fakeClient{}
	model := New(client, func() string { return "delete-1" }, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}}})
	model.focus = pageFocus{kind: focusRow, id: "a"}
	_, command := model.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	confirmation, ok := command().(ui.ConfirmationRequestMsg)
	if !ok || confirmation.OnConfirm == nil || strings.Contains(strings.ToLower(confirmation.Object), "retype") {
		t.Fatalf("confirmation=%#v", confirmation)
	}
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 8, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}}})
	_, remove := model.Update(confirmation.OnConfirm())
	model.Update(remove())
	if client.remove.IfRevision == nil || *client.remove.IfRevision != 7 || client.remove.OperationID != "delete-1" {
		t.Fatalf("remove request=%#v", client.remove)
	}
}

func TestModel_EditFormKeepsOpeningRevision(t *testing.T) {
	client := &fakeClient{updateResult: protocol.SubscriptionResult{Revision: 9, Subscription: protocol.Subscription{ID: "a", Name: "A", Enabled: true}}}
	model := New(client, func() string { return "edit-1" }, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A", Enabled: true}}})
	model.focus = pageFocus{kind: focusRow, id: "a"}
	model.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 8, Subscriptions: []protocol.Subscription{{ID: "a", Name: "Changed", Enabled: true}}})
	model.form.index = len(model.form.inputs) - 1
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, message := range command().(tea.BatchMsg) {
		if result := message(); result != nil {
			model.Update(result)
		}
	}
	if client.update.IfRevision == nil || *client.update.IfRevision != 7 {
		t.Fatalf("update request=%#v", client.update)
	}
}

type fakeClient struct {
	list         protocol.SubscriptionList
	toggle       protocol.SubscriptionEnabledRequest
	toggleResult protocol.SubscriptionResult
	toggleErr    error
	update       protocol.SubscriptionUpdateRequest
	updateResult protocol.SubscriptionResult
	remove       protocol.MutationRequest
}

func (f *fakeClient) Subscriptions(context.Context) (protocol.SubscriptionList, error) {
	return f.list, nil
}
func (f *fakeClient) AddSubscription(context.Context, protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, nil
}
func (f *fakeClient) RefreshSubscription(context.Context, string, protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, nil
}
func (f *fakeClient) UseSubscription(context.Context, string, protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, nil
}
func (f *fakeClient) SetSubscriptionEnabled(_ context.Context, _ string, request protocol.SubscriptionEnabledRequest) (protocol.SubscriptionResult, error) {
	f.toggle = request
	return f.toggleResult, f.toggleErr
}
func (f *fakeClient) UpdateSubscription(_ context.Context, _ string, request protocol.SubscriptionUpdateRequest) (protocol.SubscriptionResult, error) {
	f.update = request
	return f.updateResult, nil
}
func (f *fakeClient) RemoveSubscription(_ context.Context, _ string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	f.remove = request
	return protocol.MutationResult{}, nil
}
