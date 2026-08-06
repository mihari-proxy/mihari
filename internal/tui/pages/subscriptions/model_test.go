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

// drainCmd expands tea.BatchMsg, applies mutation results, and ignores spinner tick sleeps.
// Returns any leftover non-tick command (e.g. reload after revision conflict).
func drainCmd(t *testing.T, model *Model, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var leftover tea.Cmd
		for _, child := range batch {
			if next := drainCmd(t, model, child); next != nil {
				leftover = next
			}
		}
		return leftover
	}
	switch msg.(type) {
	case startLoadSpinMsg:
		_, _ = model.Update(msg) // arm spinning; do not follow tea.Tick
		return nil
	case loadSpinTickMsg:
		return nil
	}
	updated, next := model.Update(msg)
	if updated != model {
		t.Fatalf("model identity changed: %T", updated)
	}
	return next
}

// testRowCols is the full subscription table definition used by row-level
// tests; widths are generous so assertions see complete values.
func testRowCols() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "name", Title: ui.NameLabel, MinWidth: 10, Flex: 3, Priority: 7},
		{ID: "active", Title: ui.ActiveLabel, MinWidth: 6, Flex: 0, Priority: 6},
		{ID: "state", Title: "State", MinWidth: 8, Flex: 0, Priority: 5},
		{ID: "load", Title: ui.LoadLabel, MinWidth: 9, Flex: 0, Priority: 4},
		{ID: "traffic", Title: ui.TrafficLabel, MinWidth: 11, Flex: 1, Priority: 3},
		{ID: "lastSuccess", Title: ui.LastUpdateLabel, MinWidth: 11, Flex: 0, Priority: 2},
		{ID: "nextRefresh", Title: ui.NextUpdateLabel, MinWidth: 11, Flex: 0, Priority: 1},
	}
}

func renderTestRow(theme ui.Theme, row row) string {
	cols := testRowCols()
	widths := make([]int, len(cols))
	for index, col := range cols {
		widths[index] = max(col.MinWidth, 14)
	}
	return row.Render(theme, cols, widths)
}

func TestRows_DoNotExposeInternalGenerationOrURL(t *testing.T) {
	now := time.Unix(100, 0)
	row := rowFrom(protocol.Subscription{ID: "one", Name: "Main", Generation: 42, Cached: true, LastError: "https://example.test/?token=secret"}, false, "", now, now, "12h")
	rendered := renderTestRow(ui.DefaultTheme(), row)
	for _, forbidden := range []string{"42", "example.test", "token", "secret", "http"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("row leaked %q: %s", forbidden, rendered)
		}
	}
}

func TestRows_RenderTrafficUsage(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	theme := ui.DefaultTheme()
	sub := protocol.Subscription{
		Name: "kanata", Enabled: true, Cached: true, UpdatedAt: now.Add(-time.Hour),
		Upload: 5 << 30, Download: 5 << 30, Total: 80 << 30,
	}
	got := renderTestRow(theme, rowFrom(sub, true, "", now, now, "12h"))
	// The column uses compact quota (9G/100G), not the full IEC form.
	want := ui.FormatSubscriptionTrafficCompact(sub.Upload, sub.Download, sub.Total)
	if !strings.Contains(got, want) {
		t.Fatalf("row missing compact traffic %q:\n%s", want, got)
	}
	if strings.Contains(got, ui.FormatSubscriptionTraffic(sub.Upload, sub.Download, sub.Total)) {
		t.Fatalf("row should not use full traffic form:\n%s", got)
	}
	if !strings.Contains(got, "●") || !strings.Contains(got, "kanata") {
		t.Fatalf("row missing basics:\n%s", got)
	}
}

func TestRows_RenderLoadPhases(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	clock := time.Unix(0, 0) // ⠋
	theme := ui.DefaultTheme()
	tests := []struct {
		name         string
		subscription protocol.Subscription
		active       bool
		pending      string
		want         []string
		banned       []string
	}{
		{
			name:         "live only when active cached and applied",
			subscription: protocol.Subscription{Name: "Live", Enabled: true, AutoRefresh: true, Cached: true, UpdatedAt: now.Add(-time.Hour)},
			active:       true,
			want:         []string{"●", "Live", "Enabled", ui.LoadLiveState, "1h ago", "in 11h"},
			banned:       []string{"Ready", "Cached"},
		},
		{
			name:         "cached but not active is not live",
			subscription: protocol.Subscription{Name: "Standby", Enabled: true, AutoRefresh: true, Cached: true, UpdatedAt: now.Add(-time.Hour)},
			want:         []string{"Standby", ui.LoadCachedState, "Enabled"},
			banned:       []string{ui.LoadLiveState, "●"},
		},
		{
			name:         "missing cache",
			subscription: protocol.Subscription{Name: "Missing", Enabled: true, AutoRefresh: false},
			want:         []string{ui.LoadMissingState, "Manual"},
		},
		{
			name:         "stale cache",
			subscription: protocol.Subscription{Name: "Stale", Enabled: true, AutoRefresh: true, Cached: true, UpdatedAt: now.Add(-13 * time.Hour)},
			want:         []string{ui.LoadStaleState, "Retry pending"},
			banned:       []string{ui.LoadLiveState},
		},
		{
			name:         "fetching uses braille spinner",
			subscription: protocol.Subscription{Name: "Updating", Enabled: true},
			pending:      "refresh",
			want:         []string{"⠋", ui.LoadFetchingLabel},
		},
		{
			name:         "applying uses braille spinner",
			subscription: protocol.Subscription{Name: "Activate", Enabled: true, Cached: true, UpdatedAt: now.Add(-time.Hour)},
			pending:      "use",
			want:         []string{"⠋", ui.LoadApplyingLabel},
		},
		{
			name:         "failed after last error",
			subscription: protocol.Subscription{Name: "Broken", Enabled: true, Cached: true, UpdatedAt: now.Add(-time.Hour), LastError: "provider timeout"},
			active:       true,
			want:         []string{ui.LoadFailedState},
			banned:       []string{ui.LoadLiveState},
		},
		{
			name:         "disabled",
			subscription: protocol.Subscription{Name: "Off", Enabled: false, Cached: true},
			want:         []string{"Disabled"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := renderTestRow(theme, rowFrom(test.subscription, test.active, test.pending, now, clock, "12h"))
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Fatalf("row missing %q: %s", want, got)
				}
			}
			for _, banned := range test.banned {
				if strings.Contains(got, banned) {
					t.Fatalf("row should not contain %q: %s", banned, got)
				}
			}
		})
	}
}

func TestView_LoadHeaderAndLivePhase(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	model := New(nil, nil, func() time.Time { return now })
	model.SetSubscriptions(protocol.SubscriptionList{
		ActiveID: "a", GlobalInterval: "12h",
		Subscriptions: []protocol.Subscription{
			{ID: "a", Name: "kanata2", Enabled: true, AutoRefresh: true, Cached: true, UpdatedAt: now.Add(-9 * time.Minute)},
			{ID: "b", Name: "other", Enabled: true, Cached: true, UpdatedAt: now.Add(-time.Hour)},
		},
	})
	view := model.View()
	for _, want := range []string{ui.LoadLabel, ui.LoadLiveState, ui.LoadCachedState, "kanata2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, " Cache ") {
		t.Fatalf("header still uses Cache column:\n%s", view)
	}
}

func TestRefreshStartsBrailleLoadSpin(t *testing.T) {
	client := &fakeClient{}
	model := New(client, func() string { return "op" }, func() time.Time { return time.Unix(0, 0) })
	model.SetSubscriptions(protocol.SubscriptionList{Revision: 1, Subscriptions: []protocol.Subscription{
		{ID: "a", Name: "A", Enabled: true},
	}})
	model.focus = pageFocus{kind: focusRow, id: "a"}
	_, cmd := model.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil {
		t.Fatal("expected refresh command batch")
	}
	if model.pending["a"] != "refresh" {
		t.Fatalf("pending=%v", model.pending)
	}
	// Apply startLoadSpinMsg from the batch without running the network cmd fully:
	// drain network result too so pending clears; re-set pending to assert spinner label path.
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) < 2 {
		t.Fatalf("want batch with work+spin, got %T len=%d", msg, len(batch))
	}
	// First child is network work; run it. Second is startLoadSpin.
	_ = drainCmd(t, model, batch[0])
	// pending cleared after successful refresh — set pending again to inspect label rendering.
	model.pending["a"] = "refresh"
	model.loadSpinClock = time.Unix(0, 0)
	view := model.View()
	// Load column is 9 wide by design; "⠋ Fetching" truncates to "⠋ Fetchi…".
	if !strings.Contains(view, "Fetch") || !strings.Contains(view, "…") {
		t.Fatalf("view missing fetching label:\n%s", view)
	}
	if !strings.Contains(view, "⠋") {
		t.Fatalf("view missing braille frame:\n%s", view)
	}
}

func TestView_HeaderUsesLastUpdateAndNextUpdate(t *testing.T) {
	model := New(nil, nil, func() time.Time { return time.Unix(100, 0) })
	model.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{
		{ID: "a", Name: "Alpha", Enabled: true},
	}})
	view := model.View()
	for _, want := range []string{ui.LastUpdateLabel, ui.NextUpdateLabel} {
		if !strings.Contains(view, want) {
			t.Fatalf("header missing %q:\n%s", want, view)
		}
	}
	for _, forbidden := range []string{"Last success", "Next refresh"} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("header still contains legacy label %q:\n%s", forbidden, view)
		}
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
	railView := model.View()
	for _, line := range strings.Split(railView, "\n") {
		if strings.Contains(line, "Beta") && !strings.Contains(line, ui.FocusMarker) {
			t.Fatalf("row marker missing while rail-focused: %q", line)
		}
	}

	model.SetContentFocused(true)
	view := model.View()
	if view == railView {
		t.Fatal("content focus should change focused row styling")
	}
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
	if !strings.Contains(focused, ui.FocusMarker) {
		t.Fatalf("focused content row missing marker: %q", focused)
	}
	// RowFocus wraps the focused body; unfocused rows may still have section-border ANSI.
	// Contract: only the focused row should differ from its rail-focused counterpart.
	railFocused, railOther := "", ""
	for _, line := range strings.Split(railView, "\n") {
		switch {
		case strings.Contains(line, "Beta"):
			railFocused = line
		case strings.Contains(line, "Alpha"):
			railOther = line
		}
	}
	if focused == railFocused {
		t.Fatalf("focused row should gain RowFocus styling when content owns focus")
	}
	if other != railOther {
		t.Fatalf("unfocused row should not change with content focus:\n got %q\nwant %q", other, railOther)
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
	_ = drainCmd(t, model, command)
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
	_ = drainCmd(t, model, command)
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
	_ = drainCmd(t, model, command)
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
	reload := drainCmd(t, model, command)
	if reload == nil {
		t.Fatal("revision conflict did not reload")
	}
	_ = drainCmd(t, model, reload)
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
	_ = drainCmd(t, model, command)
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
