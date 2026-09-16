package setup

import (
	"context"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type timeoutSetupClient struct {
	*subscriptionClient
	call func(context.Context) error
}

func (c timeoutSetupClient) AddSubscription(ctx context.Context, _ protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, c.call(ctx)
}
func (c timeoutSetupClient) RefreshSubscription(ctx context.Context, _ string, _ protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, c.call(ctx)
}

func TestSubscriptionTimeout_SetupOwnsAddAndRetryCancellation(t *testing.T) {
	for _, retry := range []bool{false, true} {
		for _, stop := range []bool{false, true} {
			ctx, cancel := context.WithCancel(context.Background())
			calls := 0
			client := timeoutSetupClient{subscriptionClient: &subscriptionClient{fakeClient: &fakeClient{}}, call: func(got context.Context) error {
				calls++
				if _, ok := got.Deadline(); ok {
					t.Error("Setup imposed short deadline")
				}
				if got.Err() != context.Canceled {
					t.Error("Setup cancellation lost")
				}
				return got.Err()
			}}
			m := NewWithContext(ctx, client, nil)
			cmd := m.addSubscription("fixture", "https://example.test/sub")
			if retry {
				m.addedSubscription = &protocol.Subscription{ID: "saved"}
				cmd = m.refreshSavedSubscription()
			}
			if stop {
				m.Stop()
			} else {
				cancel()
			}
			cmd()
			cancel()
			if calls != 1 {
				t.Fatal("Setup replayed mutation")
			}
		}
	}
}
