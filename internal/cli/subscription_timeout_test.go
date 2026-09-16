package cli

import (
	"context"
	"io"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type timeoutCLIClient struct {
	*fakeSubscriptionClient
	call func(context.Context) error
}

func (c timeoutCLIClient) AddSubscription(ctx context.Context, _ protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, c.call(ctx)
}
func (c timeoutCLIClient) RefreshSubscription(ctx context.Context, _ string, _ protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{}, c.call(ctx)
}

func TestSubscriptionTimeout_CLIPropagatesCancellationWithoutReplay(t *testing.T) {
	for _, args := range [][]string{{"sub", "add", "fixture", "https://example.test/sub"}, {"sub", "refresh", "fixture"}} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		client := timeoutCLIClient{fakeSubscriptionClient: &fakeSubscriptionClient{}, call: func(got context.Context) error {
			calls++
			if _, ok := got.Deadline(); ok {
				t.Error("CLI introduced its own deadline")
			}
			cancel()
			if got.Err() != context.Canceled {
				t.Error("command context cancellation lost")
			}
			return got.Err()
		}}
		status := Execute(ctx, args, io.Discard, io.Discard, Dependencies{SubscriptionClient: client, NewOperationID: func() string { return "fixture" }})
		cancel()
		if status == ExitOK || calls != 1 {
			t.Fatalf("status=%d calls=%d", status, calls)
		}
	}
}
