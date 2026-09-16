package proxies

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"testing"
)

type warningSelectionClient struct {
	*fakeClient
	warning protocol.WarningOutcome
	calls   int
}

func (c *warningSelectionClient) SelectProxy(context.Context, string, protocol.ProxySelectionRequest) (protocol.MutationResult, error) {
	c.calls++
	return protocol.MutationResult{Revision: 42, WarningOutcome: c.warning}, nil
}
func TestProxySelection_PreservesCommittedWarning(t *testing.T) {
	warning := protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: "committed", Diagnostic: &protocol.Diagnostic{ID: "fixture:select", Detail: "token=fixture-selection", State: protocol.DiagnosticAvailable}}}}
	client := &warningSelectionClient{fakeClient: &fakeClient{}, warning: warning}
	model := New(client, func() string { return "fixture-op" })
	model.focus = FocusID{Group: "group", Node: "node"}
	message := model.selectFocused()()
	result, ok := message.(interface {
		Warnings() protocol.WarningOutcome
	})
	if !ok || len(result.Warnings().Warnings) != 1 || result.Warnings().Warnings[0].Diagnostic.Detail != "token=fixture-selection" {
		t.Fatal("committed selection warning was discarded")
	}
	if client.calls != 1 || message.(selectionResultMsg).Err() != nil {
		t.Fatal("warning changed business outcome")
	}
	origin, ok := message.(interface{ DiagnosticPage() ui.PageID })
	if !ok || origin.DiagnosticPage() != ui.PageProxies {
		t.Fatal("late selection result lost its originating page")
	}
}
func TestRoutingResult_PreservesCommittedWarning(t *testing.T) {
	var result any = routingResultMsg{status: protocol.RoutingStatus{Revision: 42, WarningOutcome: protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: "routing committed", Diagnostic: &protocol.Diagnostic{Detail: "token=fixture-routing"}}}}}}
	carrier, ok := result.(interface {
		Warnings() protocol.WarningOutcome
	})
	if !ok || len(carrier.Warnings().Warnings) != 1 || carrier.Warnings().Warnings[0].Diagnostic.Detail != "token=fixture-routing" {
		t.Fatal("routing warning lost")
	}
}
