package setup

import (
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

type referenceSetupClient struct {
	fakeClient
	requests []protocol.SubscriptionAddRequest
}

func (c *referenceSetupClient) AddSubscription(_ context.Context, r protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	c.requests = append(c.requests, r)
	if !r.AllowFileReferences {
		return protocol.SubscriptionResult{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "References require confirmation", Details: map[string]any{"confirmation_required": "file_references"}}
	}
	return protocol.SubscriptionResult{Subscription: protocol.Subscription{ID: "local", Name: r.Name, Cached: true, SourceType: "file"}}, nil
}

func TestSetup_LocalSourceToggleAndConfirmation(t *testing.T) {
	c := &referenceSetupClient{}
	n := 0
	m := New(c, func() string { n++; return fmt.Sprintf("op-%d", n) })
	m.step = stepSubscription
	m.loading = false
	m.subscriptionInputs = subscriptionInputs()
	m.subscriptionInputs[0].SetValue("local")
	m.subscriptionInputs[1].SetValue("https://fixture.test/sub")
	m.focusSubscription(2)
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.localSource || m.subscriptionInputs[1].Value() != "" || m.subscriptionInputs[0].Value() != "local" {
		t.Fatal("toggle did not clear only source")
	}
	path := filepath.Join(t.TempDir(), "main.yaml")
	m.subscriptionInputs[1].SetValue(path)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if !m.fileConfirmation || m.fileConfirmYes || m.addedSubscription != nil {
		t.Fatal("confirmation defaults or saved state invalid")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	want, _ := platform.FileURI(path)
	if len(c.requests) != 2 || c.requests[0].OperationID == c.requests[1].OperationID || c.requests[1].URL != want || !c.requests[1].AllowFileReferences || m.step != stepGeoIP {
		t.Fatalf("requests=%+v step=%d", c.requests, m.step)
	}
}

func TestSetup_ConfirmationEscapeStaysOnSubscription(t *testing.T) {
	m := New(&fakeClient{}, func() string { return "op" })
	m.step, m.loading, m.fileConfirmation = stepSubscription, false, true
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.fileConfirmation || m.step != stepSubscription {
		t.Fatalf("step=%d confirmation=%v", m.step, m.fileConfirmation)
	}
}
func TestSetup_SourceEditClearsAcknowledgement(t *testing.T) {
	for _, message := range []tea.Msg{tea.PasteMsg{Content: "https://fixture.test/new"}, tea.KeyPressMsg{Code: 'x', Text: "x"}} {
		m := New(&fakeClient{}, func() string { return "op" })
		m.step, m.loading, m.allowFileReferences = stepSubscription, false, true
		m.subscriptionInputs = subscriptionInputs()
		m.focusSubscription(1)
		m.Update(message)
		if m.allowFileReferences {
			t.Fatal("source edit retained acknowledgement")
		}
	}
}
func TestSetup_ConfirmationWrapsToFrame(t *testing.T) {
	m := New(&fakeClient{}, func() string { return "op" })
	m.SetSize(180, 100)
	m.step, m.loading, m.fileConfirmation = stepSubscription, false, true
	m.subscriptionInputs = subscriptionInputs()
	m.fileConfirmationNote = strings.Repeat("reference/path/", 18)
	lines := m.subscriptionStatusLines()
	for _, line := range lines {
		if lipgloss.Width(line) > 82 {
			t.Fatalf("line exceeds frame: %s", line)
		}
	}
	if strings.Contains(m.FooterHints(), "Space") {
		t.Fatal("confirmation has source keys")
	}
	m.fileConfirmation = false
	if !strings.Contains(m.FooterHints(), "source") {
		t.Fatalf("footer=%s", m.FooterHints())
	}
}
