package subscriptions

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"strings"
	"testing"
)

type detailClient struct {
	updateErr error
	addResult protocol.SubscriptionResult
	addErr    error
	*fakeClient
	raw     string
	state   string
	queries int
}

func (f *detailClient) AddSubscription(ctx context.Context, r protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	_, _ = f.fakeClient.AddSubscription(ctx, r)
	return f.addResult, f.addErr
}

func (f *detailClient) UpdateSubscription(ctx context.Context, id string, request protocol.SubscriptionUpdateRequest) (protocol.SubscriptionResult, error) {
	result, _ := f.fakeClient.UpdateSubscription(ctx, id, request)
	return result, f.updateErr
}

func (f *detailClient) SubscriptionURL(context.Context, string) (protocol.SubscriptionURL, error) {
	return protocol.SubscriptionURL{URL: f.raw}, nil
}
func (f *detailClient) OperationStatus(context.Context, string) (protocol.OperationStatus, error) {
	f.queries++
	return protocol.OperationStatus{State: f.state}, nil
}

func TestDetailSave_EnterOpensAndSavingLocksDialog(t *testing.T) {
	f := &detailClient{fakeClient: &fakeClient{updateResult: protocol.SubscriptionResult{Revision: 8, Subscription: protocol.Subscription{ID: "a", Name: "Changed"}}}, raw: "https://fixture.test/sub"}
	m := New(f, func() string { return "save" }, nil)
	m.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{{ID: "a", Name: "Main"}}})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form == nil {
		t.Fatal("Enter did not open editable detail")
	}
	drainCmd(t, m, cmd)
	if m.form.inputs[1].Value() != f.raw {
		t.Fatal("URL was not filled")
	}
	m.form.inputs[0].SetValue("Changed")
	m.form.index = len(m.form.inputs)
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.form == nil || !strings.Contains(m.View(), "Saving") {
		t.Fatal("saving dialog closed or not visibly saving")
	}
	if _, again := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); again != nil {
		t.Fatal("duplicate submission")
	}
	drainCmd(t, m, cmd)
	if m.form != nil || m.focus.id != "a" {
		t.Fatal("success did not close and retain selection")
	}
}

func TestDetailReveal_LateResponsePreservesInput(t *testing.T) {
	f := &detailClient{fakeClient: &fakeClient{}, raw: "https://fixture.test/original"}
	m := New(f, nil, nil)
	m.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a", Name: "Main"}}})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form == nil {
		t.Fatal("missing form")
	}
	m.form.move(1)
	m.Update(tea.PasteMsg{Content: "https://fixture.test/draft"})
	drainCmd(t, m, cmd)
	if m.form.inputs[1].Value() != "https://fixture.test/draft" {
		t.Fatal("late reveal overwrote draft")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	drainCmd(t, m, cmd)
	if m.form != nil {
		t.Fatal("late reveal reopened dialog")
	}
}

func TestDetailSave_ConflictConfirmationAndUnknownRetry(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflict", true: "unknown"}[unknown], func(t *testing.T) {
			f := &detailClient{fakeClient: &fakeClient{list: protocol.SubscriptionList{Revision: 9, Subscriptions: []protocol.Subscription{{ID: "a", Name: "Other", ProxyMode: "proxy"}}}}, state: "unknown"}
			f.updateErr = protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"}
			if unknown {
				f.updateErr = context.DeadlineExceeded
			}
			count := 0
			m := New(f, func() string { count++; return strings.Repeat("o", count) }, nil)
			m.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{{ID: "a", Name: "Main"}}})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.form == nil {
				t.Fatal("missing form")
			}
			m.form.inputs[0].SetValue("Mine")
			m.form.index = len(m.form.inputs)
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			next := drainCmd(t, m, cmd)
			for i := 0; i < 3 && next != nil; i++ {
				next = drainCmd(t, m, next)
			}
			if m.form == nil {
				t.Fatal("uncertain save closed form")
			}
			if unknown {
				if !strings.Contains(m.View(), "Submit again") {
					t.Fatal("unknown outcome lacks retry")
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			if !strings.Contains(m.View(), "Cancel") {
				t.Fatal("missing second confirmation")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
			f.updateErr = nil
			f.updateResult = protocol.SubscriptionResult{Revision: 10, Subscription: protocol.Subscription{ID: "a", Name: "Mine", ProxyMode: "proxy"}}
			_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			next = drainCmd(t, m, cmd)
			if next != nil {
				drainCmd(t, m, next)
			}
			if f.update.OperationID != "oo" || f.update.IfRevision == nil || *f.update.IfRevision != 9 || f.update.Name == nil || f.update.ProxyMode != nil {
				t.Fatal("retry did not use new ID/latest revision/changed fields")
			}
		})
	}
}

func TestDetailSave_UnchangedClosesWithoutMutation(t *testing.T) {
	f := &detailClient{fakeClient: &fakeClient{}}
	m := New(f, nil, nil)
	m.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a", Name: "Main"}}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.form.index = len(m.form.inputs)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainCmd(t, m, cmd)
	if len(f.mutations) != 0 || m.form != nil {
		t.Fatal("unchanged Save submitted a mutation")
	}
}

func TestDetailLayout_FocusedFieldVisible(t *testing.T) {
	m := New(nil, nil, nil)
	m.SetSize(68, 19)
	m.openForm(newEditForm(protocol.Subscription{Name: "Main"}), "a")
	if !strings.Contains(m.View(), "Name") {
		t.Fatal("initial focused Name is clipped")
	}
	for i := 0; i < 5; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		label := "Save"
		if i < 4 {
			label = m.form.labels[i+1]
		}
		if !strings.Contains(m.View(), label) {
			t.Fatalf("focused %s is clipped", label)
		}
	}
}

// TestDetailLayout_StatusCanScrollIntoView keeps status reachable after field navigation.
func TestDetailLayout_StatusCanScrollIntoView(t *testing.T) {
	m := New(nil, nil, nil)
	m.SetSize(68, 19)
	m.openForm(newEditForm(protocol.Subscription{Name: "Main"}), "a")
	m.form.index = len(m.form.inputs)
	m.ensureFormFocus()
	for i := 0; i < 3; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	}
	if !strings.Contains(m.View(), "In use") && !strings.Contains(m.View(), "Not in use") {
		t.Fatal("cannot scroll to status")
	}
}

func TestDetailLayout_SavingFooterDoesNotPromiseCancel(t *testing.T) {
	m := New(nil, nil, nil)
	m.openForm(newEditForm(protocol.Subscription{Name: "Main"}), "a")
	m.saveState = saveSending
	if strings.Contains(m.FooterHints(), "cancel") {
		t.Fatal("Saving footer promises cancellation")
	}
}

func TestDetailSave_AddFailedDownloadDoesNotRepeatCreation(t *testing.T) {
	f := &detailClient{fakeClient: &fakeClient{}, addResult: protocol.SubscriptionResult{Subscription: protocol.Subscription{ID: "created", Name: "Main", LastError: "download failed"}}}
	m := New(f, nil, nil)
	m.openForm(newAddForm(), "")
	m.form.inputs[0].SetValue("Main")
	m.form.inputs[1].SetValue("https://fixture.test/sub")
	m.form.index = len(m.form.inputs)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainCmd(t, m, cmd)
	if m.form != nil || m.focus.id != "created" || len(f.mutations) != 1 || !strings.Contains(m.lastError, "initial refresh failed") {
		t.Fatal("created subscription was not returned to list")
	}
}

func TestDetailSave_AddUnknownDefaultCancelAndRunningGuard(t *testing.T) {
	f := &detailClient{fakeClient: &fakeClient{list: protocol.SubscriptionList{Revision: 9}}, addErr: context.DeadlineExceeded, state: "unknown"}
	m := New(f, nil, nil)
	m.openForm(newAddForm(), "")
	m.form.inputs[0].SetValue("Main")
	m.form.inputs[1].SetValue("https://fixture.test/sub")
	m.form.index = len(m.form.inputs)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next := drainCmd(t, m, cmd)
	drainCmd(t, m, next)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.View(), "duplicate subscription") {
		t.Fatal("duplicate warning missing")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(f.mutations) != 1 || m.saveState != saveUnknown {
		t.Fatal("default Cancel retried Add")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	f.state = "running"
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	drainCmd(t, m, cmd)
	if m.saveState != saveRunning || len(f.mutations) != 1 {
		t.Fatal("running operation allowed duplicate Add")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.form != nil {
		t.Fatal("unknown save cannot be closed")
	}
}

func TestDetailReveal_ConnectionAndDialogIdentity(t *testing.T) {
	f := &detailClient{fakeClient: &fakeClient{}, raw: "https://fixture.test/original"}
	m := New(f, nil, nil)
	cmd := m.openForm(newEditForm(protocol.Subscription{Name: "A"}), "a")
	late := cmd()
	m.ObserveConnection(false)
	m.ObserveConnection(true)
	m.Update(late)
	if m.form.inputs[1].Value() != "" {
		t.Fatal("previous connection filled URL")
	}
	cmd = m.openForm(newEditForm(protocol.Subscription{Name: "A"}), "a")
	late = cmd()
	m.closeForm()
	m.openForm(newEditForm(protocol.Subscription{Name: "A"}), "a")
	m.Update(late)
	if m.form.inputs[1].Value() != "" {
		t.Fatal("previous dialog filled URL")
	}
	m.Stop()
}

func TestDetailForm_URLClearAndExplicitZeroValues(t *testing.T) {
	f := newEditForm(protocol.Subscription{Name: "Main", Interval: "6h", AutoRefresh: true, ProxyMode: "proxy"})
	f.reveal("https://fixture.test/original")
	f.move(1)
	f.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if f.valid() || f.errorText != "URL is required." {
		t.Fatal("active URL clear was not rejected")
	}
	f.Update(tea.PasteMsg{Content: "https://fixture.test/new"})
	f.inputs[2].SetValue("")
	f.inputs[3].SetValue("false")
	f.inputs[4].SetValue("")
	r := f.updateRequest("save", 1)
	if !f.valid() || r.URL == nil || r.Interval == nil || *r.Interval != "" || r.AutoRefresh == nil || *r.AutoRefresh || r.ProxyMode == nil || *r.ProxyMode != "" {
		t.Fatal("explicit empty/false fields were omitted")
	}
}

func TestDetailSave_OlderVerificationCannotClaimCurrentSuccess(t *testing.T) {
	m := New(&fakeClient{}, nil, nil)
	m.openForm(newEditForm(protocol.Subscription{ID: "a", Name: "Old"}), "a")
	m.form.inputs[0].SetValue("Mine")
	m.saveState = saveChecking
	m.querySeq = 1
	m.SetSubscriptions(protocol.SubscriptionList{Revision: 12, Subscriptions: []protocol.Subscription{{ID: "a", Name: "Newer"}}})
	m.Update(saveCheckMsg{epoch: m.dialogEpoch, seq: 1, purpose: saveUnknown, matches: true, list: protocol.SubscriptionList{Revision: 11, Subscriptions: []protocol.Subscription{{ID: "a", Name: "Mine"}}}})
	if m.form == nil || m.revision != 12 || m.subscriptions[0].Name != "Newer" {
		t.Fatal("stale query replaced newer state or claimed success")
	}
}

func TestDetailReveal_LongURLIsNotTruncatedOrSubmittedAsAnEdit(t *testing.T) {
	f := newEditForm(protocol.Subscription{Name: "Main"})
	raw := "https://fixture.test/sub?token=" + strings.Repeat("x", 4096)
	f.reveal(raw)
	if f.inputs[1].Value() != raw {
		t.Fatal("reveal silently truncated the stored URL")
	}
	f.inputs[0].SetValue("Renamed")
	if f.updateRequest("save", 1).URL != nil {
		t.Fatal("reveal was treated as an edit")
	}
}

func TestDetailLayout_PageUpAfterExcessPageDown(t *testing.T) {
	m := New(nil, nil, nil)
	m.SetSize(68, 19)
	m.openForm(newEditForm(protocol.Subscription{Name: "Main"}), "a")
	for i := 0; i < 100; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m.View()
	}
	before := m.View()
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.View() == before {
		t.Fatal("PageUp ignored after scrolling beyond the end")
	}
}

type unsentSaveError struct{}

func (unsentSaveError) Error() string        { return "credential read timed out" }
func (unsentSaveError) Unwrap() error        { return context.DeadlineExceeded }
func (unsentSaveError) OutcomeUnknown() bool { return false }

func TestDetailSave_UnsentFailureAllowsEditing(t *testing.T) {
	m := New(nil, nil, nil)
	m.openForm(newEditForm(protocol.Subscription{Name: "Main"}), "a")
	m.saveState = saveSending
	m.finishSave(mutationResultMsg{err: unsentSaveError{}})
	if m.saveState != saveEditing {
		t.Fatal("unsent failure required unknown-outcome confirmation")
	}
}

func TestDetailReveal_RetriesAfterFailedSave(t *testing.T) {
	f := &detailClient{fakeClient: &fakeClient{}, raw: "https://fixture.test/current"}
	m := New(f, nil, nil)
	cmd := m.openForm(newEditForm(protocol.Subscription{Name: "Main"}), "a")
	m.saveState = saveSending
	drainCmd(t, m, cmd)
	cmd = m.finishSave(mutationResultMsg{err: protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid"}})
	drainCmd(t, m, cmd)
	if m.form.inputs[1].Value() != f.raw {
		t.Fatal("failed save left URL unreadable after discarded reveal")
	}
}

func TestDetailSave_ConflictRequiresConfirmationEveryTime(t *testing.T) {
	for _, cancelDefault := range []bool{true, false} {
		t.Run(map[bool]string{true: "default cancel", false: "repeat conflict"}[cancelDefault], func(t *testing.T) {
			f := &detailClient{fakeClient: &fakeClient{list: protocol.SubscriptionList{Revision: 9, Subscriptions: []protocol.Subscription{{ID: "a", Name: "Other"}}}}, updateErr: protocol.APIError{Code: protocol.CodeRevisionConflict}}
			m := New(f, nil, nil)
			m.SetSubscriptions(protocol.SubscriptionList{Revision: 7, Subscriptions: []protocol.Subscription{{ID: "a", Name: "Main"}}})
			m.openForm(newEditForm(protocol.Subscription{Name: "Main"}), "a")
			m.form.inputs[0].SetValue("Mine")
			m.form.index = len(m.form.inputs)
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			drainCmd(t, m, cmd)
			if !cancelDefault {
				m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
			}
			_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			for i := 0; i < 4 && cmd != nil; i++ {
				cmd = drainCmd(t, m, cmd)
			}
			if cancelDefault {
				if m.form != nil || len(f.mutations) != 1 {
					t.Fatal("default Cancel resubmitted")
				}
				return
			}
			if m.saveState != saveConflict || m.confirmYes || len(f.mutations) != 2 {
				t.Fatal("second conflict skipped confirmation")
			}
			_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			drainCmd(t, m, cmd)
			if m.form != nil || len(f.mutations) != 2 {
				t.Fatal("second default Cancel resubmitted")
			}
		})
	}
}
