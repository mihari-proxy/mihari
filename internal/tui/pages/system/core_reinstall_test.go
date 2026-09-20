package system

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type reinstallClient struct {
	*fakeClient
	request protocol.MutationRequest
	calls   int
}

func (c *reinstallClient) ReinstallCore(_ context.Context, request protocol.MutationRequest) (protocol.CoreInstallResult, error) {
	c.calls++
	c.request = request
	return protocol.CoreInstallResult{Schema: "mihari/v1", Version: "v1.99.0", Channel: "stable", Updated: true}, nil
}

func TestSystem_OffersExplicitReinstallWhileCoreIsDegraded(t *testing.T) {
	client := &reinstallClient{fakeClient: &fakeClient{}}
	m := New(client, func() string { return "reinstall-1" })
	m.SetSnapshot(protocol.Status{Revision: 7, Health: "degraded", Capabilities: []string{protocol.CapabilityCore, protocol.CapabilityCoreReinstall}}, protocol.CoreStatus{Revision: 7, Status: "degraded", Channel: "stable"})
	m.SetMutationsEnabled(true)
	m.focusID = "core-reinstall"
	_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("degraded core has no reinstall action")
	}
	intent, ok := command().(ui.ActionIntentMsg)
	if !ok || intent.Action != "reinstall-core" || !strings.Contains(intent.Impact, "original channel") {
		t.Fatalf("incorrect repair intent: %+v", intent)
	}
	if client.calls != 0 {
		t.Fatal("core reinstall dispatched before confirmation")
	}
	result := intent.Execute().(actionResultMsg)
	if result.err != nil || client.calls != 1 || client.request.Channel != nil || client.request.OperationID != "reinstall-1" {
		t.Fatalf("reinstall did not defer original channel selection to daemon: calls=%d request=%+v err=%v", client.calls, client.request, result.err)
	}
}
