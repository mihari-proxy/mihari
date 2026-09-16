package rules

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

type warningProviderClient struct {
	*fakeClient
	calls   int
	warning protocol.WarningOutcome
	failure error
}

func (c *warningProviderClient) UpdateRuleProvider(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error) {
	c.calls++
	if c.calls == 2 {
		return protocol.MutationResult{}, c.failure
	}
	return protocol.MutationResult{Revision: 12, WarningOutcome: c.warning}, nil
}
func TestBulkProviders_PreservesWarningBeforeLaterFailure(t *testing.T) {
	warning := protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: "first committed", Diagnostic: &protocol.Diagnostic{ID: "fixture:first", Detail: "token=fixture-first", State: protocol.DiagnosticAvailable}}}}
	failure := errors.New("second failed")
	client := &warningProviderClient{fakeClient: &fakeClient{}, warning: warning, failure: failure}
	m := New(client, nil)
	m.SetProviders(protocol.RuleProviderList{Providers: []protocol.RuleProvider{{Name: "a"}, {Name: "b"}, {Name: "c"}}})
	result := m.updateAllProviders()()
	outcome, ok := result.(interface {
		Warnings() protocol.WarningOutcome
	})
	if !ok || len(outcome.Warnings().Warnings) != 1 || outcome.Warnings().Warnings[0].Diagnostic.Detail != "token=fixture-first" {
		t.Fatal("earlier committed warning lost")
	}
	if client.calls != 2 || !errors.Is(result.(providersUpdateAllResultMsg).err, failure) || result.(providersUpdateAllResultMsg).revision != 12 {
		t.Fatal("partial completion or stop-on-error changed")
	}
}
