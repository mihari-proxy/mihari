package subscriptions

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

type diagnosticPageClient struct {
	*fakeClient
	list      protocol.SubscriptionList
	revealErr error
}

func (c diagnosticPageClient) SubscriptionURL(ctx context.Context, _ string) (protocol.SubscriptionURL, error) {
	if ctx.Err() != nil {
		return protocol.SubscriptionURL{}, ctx.Err()
	}
	return protocol.SubscriptionURL{}, c.revealErr
}
func (c diagnosticPageClient) Subscriptions(ctx context.Context) (protocol.SubscriptionList, error) {
	return c.list, ctx.Err()
}

func TestSubscriptionDiagnostics_FormValidationRetainsOriginalCause(t *testing.T) {
	m := New(&fakeClient{}, nil, nil)
	m.form = newAddForm()
	m.form.inputs[0].SetValue("fixture")
	m.form.inputs[1].SetValue("https://fixture.invalid/%token=fixture-url")
	m.form.index = len(m.form.inputs)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("validation error not published")
	}
	msg, ok := cmd().(ui.DiagnosticMsg)
	if !ok || msg.Err == nil || !strings.Contains(diagnostics.Capture(msg.Err).Text, "fixture-url") {
		t.Fatal("original URL parse cause missing")
	}
	if m.saveState != saveEditing || m.form == nil {
		t.Fatal("invalid draft was submitted or cleared")
	}
}
func TestSubscriptionDiagnostics_ReadbackRetainsURLFailure(t *testing.T) {
	cause := errors.New("token=fixture-readback")
	c := diagnosticPageClient{fakeClient: &fakeClient{}, list: protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a", Name: "Main"}}}, revealErr: cause}
	m := New(c, nil, nil)
	m.form = newEditForm(protocol.Subscription{ID: "a", Name: "Main"})
	m.formID = "a"
	m.form.urlTouched = true
	m.form.inputs[1].SetValue("https://fixture.invalid/sub")
	result := m.checkSave(saveUnknown)().(saveCheckMsg)
	if !errors.Is(result.Err(), cause) {
		t.Fatal("readback URL cause was replaced by no-match")
	}
	if result.matches {
		t.Fatal("failed URL read claimed match")
	}
}
func TestSubscriptionDiagnostics_ClosedOperationsDoNotInventErrors(t *testing.T) {
	for _, kind := range []string{"row", "batch", "reveal", "readback"} {
		t.Run(kind, func(t *testing.T) {
			m := New(timeoutPageClient{call: func(ctx context.Context) error { return ctx.Err() }}, nil, nil)
			var result any
			switch kind {
			case "row":
				cmd := m.refresh("a")
				m.Stop()
				result = mutationResultFromCmd(t, cmd)
			case "batch":
				m.subscriptions = []protocol.Subscription{{ID: "a"}}
				cmd := m.refreshAll()
				m.Stop()
				result = cmd()
			default:
				m = New(diagnosticPageClient{fakeClient: &fakeClient{}}, nil, nil)
				m.form = newEditForm(protocol.Subscription{ID: "a", Name: "Main"})
				m.formID = "a"
				var cmd tea.Cmd
				if kind == "reveal" {
					cmd = m.readFormURL()
				} else {
					cmd = m.checkSave(saveUnknown)
				}
				m.closeForm()
				result = cmd()
			}
			m.Stop()
			failures, ok := result.(interface{ DiagnosticErrors() []error })
			if !ok || len(failures.DiagnosticErrors()) != 0 {
				t.Fatalf("%s cancellation became a failure", kind)
			}
		})
	}
}
