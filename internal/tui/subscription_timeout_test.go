package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	subscriptionspage "github.com/mihari-proxy/mihari/internal/tui/pages/subscriptions"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type timeoutRootClient struct {
	pageClient
	call func(context.Context) error
}

func (c timeoutRootClient) RefreshSubscription(ctx context.Context, _ string, _ protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, c.call(ctx)
}

func TestSubscriptionTimeout_RootBindsRunOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	m := newModelWithClientContext(ctx, nil, timeoutRootClient{call: func(got context.Context) error {
		calls++
		if got.Err() != context.Canceled {
			t.Error("Subs request escaped Run owner")
		}
		return got.Err()
	}})
	p := m.pages[ui.PageSubscriptions].(*subscriptionspage.Model)
	p.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "fixture"}}})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	cancel()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("missing refresh batch")
	}
	for _, child := range batch {
		child()
	}
	if calls != 1 {
		t.Fatal("refresh was not exercised")
	}
	p.Stop()
}
