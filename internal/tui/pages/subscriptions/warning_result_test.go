package subscriptions

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

type warningRefreshClient struct {
	*fakeClient
	calls   int
	warning protocol.WarningOutcome
	failure error
}

func (c *warningRefreshClient) RefreshSubscription(context.Context, string, protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	c.calls++
	if c.calls == 2 {
		return protocol.SubscriptionResult{}, c.failure
	}
	return protocol.SubscriptionResult{Revision: 12, WarningOutcome: c.warning}, nil
}
func TestBulkRefresh_PreservesWarningBeforeLaterFailure(t *testing.T) {
	warning := protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: "first committed", Diagnostic: &protocol.Diagnostic{ID: "fixture:first", Detail: "token=fixture-first", State: protocol.DiagnosticAvailable}}}}
	failure := errors.New("second failed")
	client := &warningRefreshClient{fakeClient: &fakeClient{}, warning: warning, failure: failure}
	m := New(client, nil, nil)
	m.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "a"}, {ID: "b"}, {ID: "c"}}})
	result := m.refreshAll()()
	outcome, ok := result.(interface {
		Warnings() protocol.WarningOutcome
	})
	if !ok || len(outcome.Warnings().Warnings) != 1 || outcome.Warnings().Warnings[0].Diagnostic.Detail != "token=fixture-first" {
		t.Fatal("earlier committed warning lost")
	}
	if client.calls != 2 || !errors.Is(result.(refreshAllResultMsg).err, failure) || result.(refreshAllResultMsg).revision != 12 {
		t.Fatal("partial completion or stop-on-error changed")
	}
}
