package setup

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestSetupTruncatedResponse_RequiresSettlementBeforeRetry(t *testing.T) {
	err := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "local control operation failed"}, io.ErrUnexpectedEOF)
	if !uncertainOutcome(err) {
		t.Fatal("lost response was mistaken for a confirmed mutation failure")
	}
}

func TestSetupResume_RequiredStateNotWelcomeMarker(t *testing.T) {
	for _, ready := range []bool{false, true} {
		m := loadedModel(&fakeClient{status: defaultStatus(false)})
		m.SetAutomatic(true)
		m.coreLocalLoaded = true
		m.coreLocal.LocalReady = ready
		m.portProbe = [3]portState{portOwned, portFree, portUnknown}
		cmd := m.resumeFromState()
		if ready {
			if cmd == nil {
				t.Fatal("ready core was held by optional setup")
			}
			if _, ok := cmd().(ReadyMsg); !ok {
				t.Fatal("expected ready route")
			}
		} else if m.step != stepCore {
			t.Fatal("missing core did not resume at core")
		}
	}
}

func TestSetupComplete_DoesNotResubmitSavedEndpoints(t *testing.T) {
	f := &fakeClient{status: defaultStatus(false)}
	m := loadedModel(f)
	m.complete()()
	if f.update.Complete == nil || f.update.MixedAddr != nil || f.update.ControllerAddr != nil || f.update.WebAddr != nil {
		t.Fatal("welcome completion resubmitted endpoint configuration")
	}
}

func TestSetupUnknownResult_BlocksMutationUntilRechecked(t *testing.T) {
	f := &fakeClient{status: defaultStatus(false)}
	m := loadedModel(f)
	m.step, m.resultUnknown = stepCore, true
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.step != stepCore {
		t.Fatal("unknown operation result permitted leaving its recovery step")
	}
}

func TestSetupCoreUnknown_RechecksInsteadOfInstalling(t *testing.T) {
	f := &fakeClient{status: defaultStatus(false)}
	m := loadedModel(f)
	m.step, m.coreLocalLoaded = stepCore, false
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("missing recheck")
	}
	m.Update(cmd())
	if f.installCalls != 0 || !m.coreLocalLoaded {
		t.Fatal("unknown core state started an install")
	}
}

func TestSetupPortSuggestions_PreserveOwnedUnknownAndNeverWrap(t *testing.T) {
	current := [3]string{"127.0.0.1:65535", "127.0.0.1:10001", "127.0.0.1:10002"}
	got := findAvailablePortsForStates(current, [3]portState{portOccupied, portOwned, portUnknown})
	if got != current {
		t.Fatal("port suggestion wrapped the range or changed a non-foreign port")
	}
}

type observingClient struct {
	*fakeClient
	state   string
	queries int
}

type subscriptionClient struct {
	*fakeClient
	profiles  []protocol.Subscription
	refreshes int
}

func (f *subscriptionClient) Subscriptions(context.Context) (protocol.SubscriptionList, error) {
	return protocol.SubscriptionList{Subscriptions: f.profiles}, nil
}
func (f *subscriptionClient) RefreshSubscription(_ context.Context, id string, _ protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	f.refreshes++
	return protocol.SubscriptionResult{Revision: 8, Subscription: protocol.Subscription{ID: id, Cached: true}}, nil
}

func TestSetupExistingSubscription_ShowsOverviewBeforeContinuing(t *testing.T) {
	f := &subscriptionClient{fakeClient: &fakeClient{status: defaultStatus(false)}, profiles: []protocol.Subscription{{ID: "existing", LastError: "download failed"}}}
	m := New(f, nil)
	m.Update(m.Load()())
	m.Update(actionResultMsg{next: stepSubscription})
	if m.step != stepSubscription || f.addCalls != 0 {
		t.Fatal("existing subscription overview was skipped")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		m.Update(cmd())
	}
	if m.step != stepGeoIP || f.addCalls != 0 || f.refreshes != 0 {
		t.Fatal("continuing the overview mutated an existing subscription")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.step != stepSubscription {
		t.Fatal("back navigation skipped the subscription overview")
	}
}

func TestSetupPartialSubscription_RetryRefreshesSameID(t *testing.T) {
	f := &subscriptionClient{fakeClient: &fakeClient{status: defaultStatus(false), subscriptionResult: protocol.SubscriptionResult{Subscription: protocol.Subscription{ID: "saved", LastError: "download failed"}}}}
	m := New(f, nil)
	m.Update(m.Load()())
	m.step = stepSubscription
	m.Update(m.addSubscription("Name", "https://example.test/sub")())
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("missing refresh retry")
	}
	m.Update(cmd())
	if f.addCalls != 1 || f.refreshes != 1 || m.step != stepGeoIP || m.addedSubscription.ID != "saved" {
		t.Fatal("partial success was re-added instead of refreshed")
	}
}

func TestSetupErrorDetails_RedactsURLAndDoesNotExposeRawCause(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	secretURL := "https://example.test/sub?token=private-token"
	m.subscriptionInputs[1].SetValue(secretURL)
	m.fail("Download", protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "failed " + secretURL})
	m.SetSize(86, 24)
	for _, text := range []string{m.errorDetail, m.lastError, m.errorAdvice, m.View()} {
		if strings.Contains(text, "private-token") || strings.Contains(text, secretURL) {
			t.Fatal("diagnostic text exposed subscription credentials")
		}
	}
}

func TestSetupRevisionConflict_RetainsInputAndFocus(t *testing.T) {
	f := &fakeClient{status: defaultStatus(false)}
	m := loadedModel(f)
	m.inputs[2].SetValue("127.0.0.1:19291")
	m.focusEndpoint(2)
	_, cmd := m.Update(actionResultMsg{err: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"}})
	if cmd == nil {
		t.Fatal("missing reload")
	}
	m.Update(cmd())
	if m.inputs[2].Value() != "127.0.0.1:19291" || m.focusedField != 2 {
		t.Fatal("state refresh discarded input or focus")
	}
}

func TestSetupReview_ExistingUnreadySubscriptionIsNotShownAsReady(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.addedSubscription = &protocol.Subscription{ID: "existing", Name: "Main", LastError: "download failed"}
	if !strings.Contains(m.subscriptionSummary(), "not ready") {
		t.Fatal("existing unready subscription has no warning")
	}
}

func TestSetupWaitingRestart_ReturnToEditCannotBypassRestart(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.waitingRestart, m.status.RestartRequired = true, true
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.waitingRestart {
		t.Fatal("cannot return to endpoint editing")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.waitingRestart || m.step != stepEndpoints {
		t.Fatal("unchanged saved endpoints bypassed required restart")
	}
}

func (f *observingClient) OperationStatus(context.Context, string) (protocol.OperationStatus, error) {
	f.queries++
	return protocol.OperationStatus{State: f.state}, nil
}

func TestSetupSettlement_RespectsAdvertisedCapability(t *testing.T) {
	f := &observingClient{fakeClient: &fakeClient{status: defaultStatus(false)}, state: "finished"}
	m := loadedModel(f.fakeClient)
	m.client, m.step = f, stepCore
	m.ObserveDaemon(protocol.Status{}, protocol.CoreStatus{})
	m.Update(m.settle()())
	if f.queries != 0 || !m.resultUnknown || !strings.Contains(m.View(), "does not support operation status") {
		t.Fatal("old daemon was queried without the operation-status capability")
	}
	m.ObserveDaemon(protocol.Status{Capabilities: []string{protocol.OperationStatusCapability}}, protocol.CoreStatus{})
	m.Update(m.settle()())
	if f.queries != 1 || m.resultUnknown {
		t.Fatal("updated daemon could not confirm settlement")
	}
}

func TestSetupCancellation_EndpointsOnlyNeedsEndpointReadback(t *testing.T) {
	f := &observingClient{fakeClient: &fakeClient{status: defaultStatus(false), coreErr: context.Canceled}, state: "finished"}
	m := loadedModel(f.fakeClient)
	m.client = f
	m.step = stepEndpoints
	m.Update(m.settle()())
	if m.resultUnknown {
		t.Fatal("unavailable core blocked settlement of an endpoint-only operation")
	}
}

func TestSetupSettlement_OnlyMentionsCancellationWhenRequested(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		m := loadedModel(&fakeClient{status: defaultStatus(false)})
		m.step, m.cancelRequested = stepCore, cancelled
		m.fail("Lost response", io.EOF)
		m.Update(settlementMsg{gen: m.executionGen, confirmed: true, status: defaultStatus(false)})
		if m.lastError != "" || m.errorDetail != "" {
			t.Fatal("confirmed settlement was rendered as a failure")
		}
		for _, framed := range []bool{false, true} {
			if framed {
				m.SetSize(86, 24)
			}
			view := m.View()
			if !strings.Contains(view, "Operation ended") || strings.Contains(view, "Cancellation requested") != cancelled {
				t.Fatalf("wrong settlement copy for cancelled=%v:\n%s", cancelled, view)
			}
		}
	}
}

func TestSetupSettlement_DeletedSubscriptionDoesNotSurviveReadback(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step = stepSubscription
	m.addedSubscription = &protocol.Subscription{ID: "deleted", Name: "Removed"}
	m.Update(settlementMsg{gen: m.executionGen, confirmed: true, status: defaultStatus(false)})
	if m.addedSubscription != nil || m.hasSubscriptions() || m.subscriptionNeedsRetry() {
		t.Fatal("deleted subscription survived the authoritative readback")
	}
	if !strings.Contains(m.View(), "No subscriptions") {
		t.Fatal("empty catalog did not restore the initial subscription form")
	}
}

func TestSetupSettlement_OtherStepPreservesSubscription(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step = stepCore
	m.addedSubscription = &protocol.Subscription{ID: "saved", Name: "Saved"}
	m.Update(settlementMsg{gen: m.executionGen, confirmed: true, status: defaultStatus(false)})
	if m.addedSubscription == nil || m.addedSubscription.ID != "saved" {
		t.Fatal("unrelated settlement cleared a subscription without reading it")
	}
}

func TestSetupFailure_ShowsSafeCause(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step = stepCore
	m.Update(actionResultMsg{err: protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "core download request failed"}})
	if !strings.Contains(m.View(), "core download request failed") {
		t.Fatal("specific safe failure was discarded")
	}
}

func TestSetupLayout_FitsAssignedWidth(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.SetSize(70, 20)
	m.Update(actionResultMsg{err: protocol.APIError{Code: protocol.CodeNetworkFailure, Message: strings.Repeat("diagnostic ", 100)}})
	if lipgloss.Width(m.View()) > 70 || lipgloss.Height(m.View()) > 20 {
		t.Fatal("setup content overflows the assigned area")
	}
}

func TestSetupLayout_AllStepsFitCompactAndWideTerminals(t *testing.T) {
	for _, size := range [][2]int{{70, 20}, {98, 28}, {158, 43}} {
		m := loadedModel(&fakeClient{status: defaultStatus(false)})
		m.SetSize(size[0], size[1])
		for current := stepEndpoints; current <= stepReview; current++ {
			m.step = current
			view := m.View()
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("layout overflow at step %d, size %v", current, size)
			}
		}
	}
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step = stepCore
	m.SetSize(70, 20)
	m.fail("Install core", protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "Core download request failed."})
	view := m.View()
	if lipgloss.Width(view) > 70 || lipgloss.Height(view) > 20 {
		t.Fatal("failure view overflows compact layout")
	}
}

func TestSetupBusy_EscapeOffersCancellation(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false)})
	m.step = stepCore
	_, start := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	defer func() { m.Update(start()) }()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("busy setup silently swallowed Escape")
	}
	if _, ok := cmd().(ui.ConfirmationRequestMsg); !ok {
		t.Fatal("missing cancellation confirmation")
	}
}

func TestSetupInstall_CommandDoesNotWritePageState(t *testing.T) {
	m := loadedModel(&fakeClient{status: defaultStatus(false), installResult: protocol.CoreInstallResult{Version: "v1.19.0"}})
	msg := m.installCore()()
	if m.coreResult.Version != "" {
		t.Fatal("async command wrote page state outside Update")
	}
	m.Update(msg)
	if m.coreResult.Version != "v1.19.0" {
		t.Fatal("Update did not retain install result")
	}
}

func TestSetupSubscription_PartialSuccessDoesNotAdvance(t *testing.T) {
	f := &fakeClient{status: defaultStatus(false), subscriptionResult: protocol.SubscriptionResult{Subscription: protocol.Subscription{ID: "registered", Name: "Main", LastError: "subscription download failed"}}}
	m := loadedModel(f)
	m.step = stepSubscription
	m.subscriptionInputs[0].SetValue("Main")
	m.subscriptionInputs[1].SetValue("https://example.test/sub")
	m = runKeyCommand(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.step != stepSubscription || m.addedSubscription == nil || m.addedSubscription.ID != "registered" {
		t.Fatal("registered-but-not-ready subscription was not retained for recovery")
	}
	if !strings.Contains(m.View(), "subscription download failed") {
		t.Fatal("partial failure was hidden")
	}
}
