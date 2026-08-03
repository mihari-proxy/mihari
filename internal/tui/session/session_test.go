package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func TestSession_OneUpstreamPerStreamAndStopsAllGoroutines(t *testing.T) {
	fake := newFakeClient()
	session := New(fake, Options{Backoff: func(int) time.Duration { return 0 }})
	ctx, cancel := context.WithCancel(context.Background())
	events := session.Start(ctx)
	waitForEvent(t, events, EventConnected)
	for range 4 {
		select {
		case <-fake.started:
		case <-time.After(3 * time.Second):
			t.Fatal("streams did not start")
		}
	}
	cancel()
	session.Close()
	for _, kind := range []string{"connections", "logs", "memory", "traffic"} {
		if got := fake.streamCallCount(kind); got != 1 {
			t.Fatalf("%s calls=%d", kind, got)
		}
	}
	select {
	case _, open := <-events:
		if open {
			for range events {
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("event channel did not close")
	}
}

func TestSession_ReportsReconnectBeforeConnected(t *testing.T) {
	fake := newFakeClient()
	fake.statusFailures = 1
	session := New(fake, Options{Backoff: func(int) time.Duration { return 0 }})
	events := session.Start(context.Background())
	defer session.Close()
	waitForEvent(t, events, EventReconnecting)
	waitForEvent(t, events, EventConnected)
}

func TestPutLatestCoalescesTraffic(t *testing.T) {
	slot := make(chan Event, 1)
	ctx := context.Background()
	putLatest(ctx, slot, Event{Kind: EventTraffic, Traffic: protocol.TrafficSample{Up: 1}})
	putLatest(ctx, slot, Event{Kind: EventTraffic, Traffic: protocol.TrafficSample{Up: 2}})
	if got := (<-slot).Traffic.Up; got != 2 {
		t.Fatalf("up=%d", got)
	}
}

func TestPutOrderedUnblocksWhenSessionCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := make(chan Event, 1)
	queue <- Event{Kind: EventLog}
	done := make(chan bool, 1)
	go func() { done <- putOrdered(ctx, queue, Event{Kind: EventLog}) }()
	cancel()
	select {
	case sent := <-done:
		if sent {
			t.Fatal("event sent after cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ordered producer remained blocked")
	}
}

func waitForEvent(t *testing.T, events <-chan Event, want EventKind) Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event, open := <-events:
			if !open {
				t.Fatalf("events closed before %s", want)
			}
			if event.Kind == want {
				return event
			}
		case <-deadline:
			t.Fatalf("did not receive %s", want)
		}
	}
}

type fakeClient struct {
	mu             sync.Mutex
	statusFailures int
	streamCalls    map[string]int
	started        chan string
}

func newFakeClient() *fakeClient {
	return &fakeClient{streamCalls: make(map[string]int), started: make(chan string, 16)}
}

func (f *fakeClient) Status(context.Context) (protocol.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusFailures > 0 {
		f.statusFailures--
		return protocol.Status{}, errors.New("offline")
	}
	return protocol.Status{Schema: "mihari/v1", Revision: 3}, nil
}

func (f *fakeClient) Stream(ctx context.Context, kind string, _ func(protocol.StreamEvent) error) error {
	f.mu.Lock()
	f.streamCalls[kind]++
	f.mu.Unlock()
	f.started <- kind
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeClient) streamCallCount(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streamCalls[kind]
}
