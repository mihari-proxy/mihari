package session

import (
	"context"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestPoll_StartupKeepsStatusFlowingBeforeSnapshots(t *testing.T) {
	client := newFakeClient()
	s := New(client, Options{})
	status := protocol.Status{Capabilities: []string{protocol.CapabilityCore, protocol.CapabilityLogging}, StartupNetwork: &protocol.StartupNetworkStatus{SystemProxyApplying: true}}
	if err := s.pollStatus(context.Background(), status); err != nil {
		t.Fatal(err)
	}
	if event := <-s.control; event.Kind != EventStatus || event.Status.StartupNetwork == nil {
		t.Fatalf("event=%+v", event)
	}
	if client.coreCalls != 0 || client.loggingCalls != 0 {
		t.Fatalf("startup status blocked behind snapshots: core=%d logging=%d", client.coreCalls, client.loggingCalls)
	}
	status.StartupNetwork = nil
	if err := s.pollStatus(context.Background(), status); err != nil {
		t.Fatal(err)
	}
	if client.coreCalls != 1 || client.loggingCalls != 1 {
		t.Fatal("snapshots did not resume after startup")
	}
}

func TestSession_StartupConnectsAndPollsBeforeOpeningStreams(t *testing.T) {
	client := newFakeClient()
	client.status = protocol.Status{Schema: "mihari/v1", Capabilities: []string{protocol.CapabilityCore}, StartupNetwork: &protocol.StartupNetworkStatus{TunApplying: true}}
	s := New(client, Options{PollInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	events := s.Start(ctx)
	t.Cleanup(func() { cancel(); s.Close() })
	waitForEvent(t, events, EventConnected)
	// A second status proves startup is polling without waiting for core streams.
	waitForEvent(t, events, EventStatus)
	client.mu.Lock()
	calls := len(client.streamCalls)
	client.status.StartupNetwork = nil
	client.mu.Unlock()
	if calls != 0 {
		t.Fatal("opened core streams while initial network application was pending")
	}
	for {
		event := waitForEvent(t, events, EventStatus)
		if event.Status.StartupNetwork == nil {
			break
		}
	}
	waitForStreamStarts(t, client.started, 4)
}
