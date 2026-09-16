package connections

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

type warningCloseClient struct {
	*fakeConnectionsClient
	calls   int
	warning protocol.WarningOutcome
}

func (c *warningCloseClient) CloseConnection(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error) {
	c.calls++
	return protocol.MutationResult{Revision: 12, WarningOutcome: c.warning}, nil
}
func (c *warningCloseClient) CloseAllConnections(context.Context, protocol.MutationRequest) (protocol.MutationResult, error) {
	c.calls++
	return protocol.MutationResult{Revision: 12, WarningOutcome: c.warning}, nil
}
func TestConnectionClose_PreservesCommittedWarning(t *testing.T) {
	warning := protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: "committed", Diagnostic: &protocol.Diagnostic{ID: "fixture:close", Detail: "token=fixture-close", State: protocol.DiagnosticAvailable}}}}
	for _, all := range []bool{false, true} {
		client := &warningCloseClient{fakeConnectionsClient: &fakeConnectionsClient{}, warning: warning}
		m := New(client, nil)
		command := m.closeConnection("fixture")
		if all {
			command = m.closeAllConnections()
		}
		result := command()
		outcome, ok := result.(interface {
			Warnings() protocol.WarningOutcome
		})
		if !ok || len(outcome.Warnings().Warnings) != 1 || outcome.Warnings().Warnings[0].Diagnostic.Detail != "token=fixture-close" {
			t.Fatal("successful close warning lost")
		}
		if client.calls != 1 || result.(closeResultMsg).Err() != nil {
			t.Fatal("warning changed successful outcome")
		}
	}
}
