package setup

import (
	"context"
	"fmt"
	"path/filepath"
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
