package subscriptions

import (
	"context"
	"reflect"
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

func TestView_FocusedSubscriptionRowIsHighlightedOnlyWhenContentFocused(t *testing.T) {
	model := New(nil, nil, func() time.Time { return time.Unix(100, 0) })
	model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{
		{ID: "a", Name: "Alpha", Enabled: true},
		{ID: "b", Name: "Beta", Enabled: true},
	}})
	model.focus = pageFocus{kind: focusRow, id: "b"}

	model.SetContentFocused(false)
	view := model.View()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Beta") {
			if !strings.Contains(line, ">") {
				t.Fatalf("row marker missing while rail-focused: %q", line)
			}
			if strings.Contains(line, "\x1b[") {
				t.Fatalf("row should not use accent while rail owns focus: %q", line)
			}
		}
	}

	model.SetContentFocused(true)
	view = model.View()
	var focused, other string
	for _, line := range strings.Split(view, "\n") {
		switch {
		case strings.Contains(line, "Beta"):
			focused = line
		case strings.Contains(line, "Alpha"):
			other = line
		}
	}
	if focused == "" || other == "" {
		t.Fatalf("missing rows in view:\n%s", view)
	}
	if !strings.Contains(focused, ">") || !strings.Contains(focused, "\x1b[") {
		t.Fatalf("focused content row missing color highlight: %q", focused)
	}
	if strings.Contains(other, "\x1b[") {
		t.Fatalf("unfocused row unexpectedly styled: %q", other)
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
	client := &fakeClient{}
	model := New(client, func() string { return "sub-op" }, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 3, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A", Enabled: true, Cached: true}, {ID: "b", Name: "B", Enabled: true}}})
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

func TestModel_RefreshFocusedAndConfirmRefreshAll(t *testing.T) {
	list := protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{
		{ID: "a", Name: "A", Enabled: true},
		{ID: "b", Name: "B", Enabled: true},
	}}
	client := &fakeClient{list: list}
	model := New(client, func() string { return "refresh-op" }, nil)
	model.SetSubscriptions(list)
	model.focus = pageFocus{kind: focusRow, id: "a"}
	_, command := model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if command == nil || model.pending["a"] != "refresh" {
		t.Fatalf("r command=%v pending=%v", command != nil, model.pending)
	}
	updated, _ := model.Update(command())
	model = updated.(*Model)
	if len(client.refreshed) != 1 || client.refreshed[0] != "a" || model.revision != 1 {
		t.Fatalf("refreshed=%v revision=%d", client.refreshed, model.revision)
	}

	_, command = model.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if command == nil {
		t.Fatal("ctrl+r did not request confirmation")
	}
	confirmation, ok := command().(ui.ActionIntentMsg)
	if !ok || confirmation.Action != ui.ActionRefreshAllSubscriptions || confirmation.Execute == nil {
		t.Fatalf("confirmation=%#v", confirmation)
	}
	if len(model.pending) != 0 {
		t.Fatalf("pending before confirmation: %v", model.pending)
	}
	// Advance the authoritative list revision so the post-refresh reload is visible.
	client.list.Revision = 9
	updated, reload := model.Update(confirmation.Execute())
	model = updated.(*Model)
	if reload == nil {
		t.Fatal("refresh-all did not reload list")
	}
	updated, _ = model.Update(reload())
	model = updated.(*Model)
	if !reflect.DeepEqual(client.refreshed, []string{"a", "a", "b"}) {
		t.Fatalf("refreshed=%v", client.refreshed)
	}
	if model.revision != 9 {
		t.Fatalf("revision after refresh-all=%d", model.revision)
	}
}

func TestModel_RefreshFailureShowsProtocolMessage(t *testing.T) {
	client := &fakeClient{refreshErr: protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid subscription YAML"}}
	model := New(client, func() string { return "refresh-op" }, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}}})
	model.focus = pageFocus{kind: focusRow, id: "a"}
	_, command := model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	updated, _ := model.Update(command())
	model = updated.(*Model)
	if model.lastError != "invalid subscription YAML" {
		t.Fatalf("lastError=%q", model.lastError)
	}
}

func TestModel_RefreshAllStopsOnFirstErrorAndReloadsOnSuccess(t *testing.T) {
	list := protocol.SubscriptionList{Revision: 3, Subscriptions: []protocol.Subscription{
		{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
	}}
	client := &fakeClient{list: list}
	// Fail after the first successful refresh so we can assert early stop.
	client.refreshHook = func(id string, call int) error {
		if call > 1 {
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "provider unavailable"}
		}
		return nil
	}
	model := New(client, func() string { return "refresh-all" }, nil)
	model.SetSubscriptions(list)
	_, command := model.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	confirmation := command().(ui.ActionIntentMsg)
	updated, follow := model.Update(confirmation.Execute())
	model = updated.(*Model)
	if follow != nil {
		t.Fatal("failed refresh-all should not reload list")
	}
	if model.lastError != "provider unavailable" {
		t.Fatalf("lastError=%q", model.lastError)
	}
	if !reflect.DeepEqual(client.refreshed, []string{"a", "b"}) {
		t.Fatalf("refreshed=%v want [a b] (stop after first failure)", client.refreshed)
	}

	// Success path: every ID refreshed and list reloaded.
	client = &fakeClient{list: protocol.SubscriptionList{Revision: 11, Subscriptions: list.Subscriptions}}
	model = New(client, func() string { return "refresh-all-ok" }, nil)
	model.SetSubscriptions(list)
	_, command = model.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	confirmation = command().(ui.ActionIntentMsg)
	updated, reload := model.Update(confirmation.Execute())
	model = updated.(*Model)
	if reload == nil {
		t.Fatal("successful refresh-all should reload")
	}
	updated, _ = model.Update(reload())
	model = updated.(*Model)
	if !reflect.DeepEqual(client.refreshed, []string{"a", "b", "c"}) {
		t.Fatalf("refreshed=%v", client.refreshed)
	}
	if model.revision != 11 {
		t.Fatalf("revision=%d", model.revision)
	}
}

func TestModel_RevisionConflictOnRefreshAllReloads(t *testing.T) {
	list := protocol.SubscriptionList{Revision: 4, Subscriptions: []protocol.Subscription{
		{ID: "a", Name: "A"}, {ID: "b", Name: "B"},
	}}
	client := &fakeClient{
		list:       protocol.SubscriptionList{Revision: 12, Subscriptions: list.Subscriptions},
		refreshErr: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"},
	}
	model := New(client, func() string { return "refresh-all" }, nil)
	model.SetSubscriptions(list)
	_, command := model.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	confirmation := command().(ui.ActionIntentMsg)
	updated, reload := model.Update(confirmation.Execute())
	model = updated.(*Model)
	if reload == nil {
		t.Fatal("revision conflict did not reload")
	}
	if model.lastError != ui.SubscriptionChangedMessage {
		t.Fatalf("lastError=%q", model.lastError)
	}
	updated, _ = model.Update(reload())
	model = updated.(*Model)
	if model.revision != 12 {
		t.Fatalf("revision=%d", model.revision)
	}
}

func TestModel_FooterHintsAreContextual(t *testing.T) {
	model := New(nil, nil, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}}})
	if hints := model.FooterHints(); !strings.Contains(hints, "r refresh") || !strings.Contains(hints, "Ctrl+R") {
		t.Fatalf("list footer=%q", hints)
	}
	model.detail = &detailState{subscription: protocol.Subscription{ID: "a", Name: "A"}}
	if hints := model.FooterHints(); !strings.Contains(hints, "Esc") {
		t.Fatalf("detail footer=%q", hints)
	}
	model.detail = nil
	model.form = newAddForm()
	if hints := model.FooterHints(); hints != ui.FormHelp {
		t.Fatalf("form footer=%q", hints)
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
	confirmation, ok := command().(ui.ActionIntentMsg)
	if !ok || confirmation.Execute == nil || confirmation.Action != ui.ActionDeleteSubscription || strings.Contains(strings.ToLower(confirmation.Object), "retype") {
		t.Fatalf("confirmation=%#v", confirmation)
	}
	if model.pending["a"] != "" {
		t.Fatalf("delete marked pending before confirmation: %v", model.pending)
	}
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 8, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}}})
	model.Update(confirmation.Execute())
	if client.remove.IfRevision == nil || *client.remove.IfRevision != 7 || client.remove.OperationID != "delete-1" {
		t.Fatalf("remove request=%#v", client.remove)
	}
}

func TestModel_DeleteRevisionConflictReloadsAndPreservesFocus(t *testing.T) {
	client := &fakeClient{
		list:      protocol.SubscriptionList{Revision: 9, Subscriptions: []protocol.Subscription{{ID: "b", Name: "B"}, {ID: "a", Name: "A"}}},
		removeErr: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"},
	}
	model := New(client, func() string { return "delete-1" }, nil)
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}})
	model.focus = pageFocus{kind: focusRow, id: "a"}
	_, command := model.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	confirmation, ok := command().(ui.ActionIntentMsg)
	if !ok || confirmation.Action != ui.ActionDeleteSubscription || confirmation.Execute == nil {
		t.Fatalf("confirmation=%#v", confirmation)
	}
	// Another client (CLI/Web) edits the list while confirmation is open.
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 8, Subscriptions: []protocol.Subscription{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}})
	conflictPage, reload := model.Update(confirmation.Execute())
	if reload == nil {
		t.Fatal("revision conflict did not trigger a reload instead of a blind retry")
	}
	if conflictPage.(*Model).lastError != ui.SubscriptionChangedMessage {
		t.Fatalf("revision conflict did not render a toast: %q", conflictPage.(*Model).lastError)
	}
	if client.remove.IfRevision == nil || *client.remove.IfRevision != 7 {
		t.Fatalf("remove used non-captured revision=%#v", client.remove)
	}
	model.Update(reload())
	if model.revision != 9 || model.focus.id != "a" {
		t.Fatalf("revision=%d focus=%#v", model.revision, model.focus)
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
	list          protocol.SubscriptionList
	toggle        protocol.SubscriptionEnabledRequest
	toggleResult  protocol.SubscriptionResult
	toggleErr     error
	update        protocol.SubscriptionUpdateRequest
	updateResult  protocol.SubscriptionResult
	remove        protocol.MutationRequest
	removeErr     error
	refreshed     []string
	refreshResult protocol.SubscriptionResult
	refreshErr    error
	refreshRev    uint64
	refreshHook   func(id string, call int) error
}

func (f *fakeClient) Subscriptions(context.Context) (protocol.SubscriptionList, error) {
	return f.list, nil
}
func (f *fakeClient) AddSubscription(context.Context, protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, nil
}
func (f *fakeClient) RefreshSubscription(_ context.Context, id string, _ protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	f.refreshed = append(f.refreshed, id)
	if f.refreshHook != nil {
		if err := f.refreshHook(id, len(f.refreshed)); err != nil {
			return protocol.SubscriptionResult{}, err
		}
	} else if f.refreshErr != nil {
		return protocol.SubscriptionResult{}, f.refreshErr
	}
	f.refreshRev++
	result := f.refreshResult
	if result.Subscription.ID == "" {
		result.Subscription = protocol.Subscription{ID: id, Name: id, Cached: true}
	} else {
		result.Subscription.ID = id
	}
	result.Revision = f.refreshRev
	return result, nil
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
	return protocol.MutationResult{}, f.removeErr
}
