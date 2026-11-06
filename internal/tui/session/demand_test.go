package session

import (
	"context"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type slowCoreClient struct {
	*fakeClient
	entered chan struct{}
}

func (c *slowCoreClient) Core(ctx context.Context) (protocol.CoreStatus, error) {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return protocol.CoreStatus{}, ctx.Err()
}
func TestSession_ConnectsAndPollsHealthWhileCoreIsSlow(t *testing.T) {
	fake := newFakeClient()
	fake.status = protocol.Status{Schema: "mihari/v1", Capabilities: []string{protocol.CapabilityCore}}
	client := &slowCoreClient{fakeClient: fake, entered: make(chan struct{}, 1)}
	s := New(client, Options{PollInterval: 10 * time.Millisecond})
	events := s.Start(t.Context())
	defer s.Close()
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("core query not started")
	}
	connected := false
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	for !connected {
		select {
		case event := <-events:
			connected = event.Kind == EventConnected
		case <-timer.C:
			t.Fatal("slow core blocked Connected")
		}
	}
	waitForStatusCallAfter(t, fake, 1)
}
