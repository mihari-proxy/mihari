package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
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

func TestSession_PollsStateWhileStreamsStayConnected(t *testing.T) {
	fake := newFakeClient()
	fake.status = protocol.Status{Schema: "mihari/v1", Capabilities: []string{protocol.CapabilityCore}}
	session := New(fake, Options{Backoff: func(int) time.Duration { return 0 }, PollInterval: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := session.Start(ctx)
	waitForEvent(t, events, EventConnected)
	// Core must keep being re-polled while the push streams remain connected;
	// a stale single snapshot here is the starting-stuck regression.
	deadline := time.After(3 * time.Second)
	for {
		fake.mu.Lock()
		calls := fake.coreCalls
		fake.mu.Unlock()
		if calls >= 3 {
			return
		}
		select {
		case _, open := <-events:
			if !open {
				t.Fatalf("events closed while streams stayed connected (core calls=%d)", calls)
			}
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatalf("core was polled only %d times while streams stayed connected", calls)
		}
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

func TestSession_LoadsCapabilitySnapshotsBeforeConnected(t *testing.T) {
	fake := newFakeClient()
	fake.status = protocol.Status{Schema: "mihari/v1", Capabilities: []string{
		protocol.CapabilityCore, protocol.CapabilitySubscriptions, protocol.CapabilityProxies,
		protocol.CapabilityRules, protocol.CapabilityRuleProviders,
	}}
	session := New(fake, Options{Backoff: func(int) time.Duration { return 0 }})
	events := session.Start(context.Background())
	defer session.Close()
	waitForEvent(t, events, EventStatus)
	waitForEvent(t, events, EventCore)
	waitForEvent(t, events, EventSubscriptions)
	waitForEvent(t, events, EventProxies)
	waitForEvent(t, events, EventRules)
	waitForEvent(t, events, EventRuleProviders)
	waitForEvent(t, events, EventConnected)
}

func TestSession_LoadsTUIPreferencesBeforeConnected(t *testing.T) {
	fake := newFakeClient()
	fake.status = protocol.Status{Schema: "mihari/v1", Capabilities: []string{protocol.CapabilityPreferences}}
	fake.preferences = protocol.TUIPreferences{Schema: "mihari/v1", Revision: 7, ConnectionsColumns: []string{"host", "chain"}}
	session := New(fake, Options{Backoff: func(int) time.Duration { return 0 }})
	events := session.Start(context.Background())
	defer session.Close()
	event := waitForEvent(t, events, EventPreferences)
	if event.Preferences.Revision != 7 || len(event.Preferences.ConnectionsColumns) != 2 {
		t.Fatalf("preferences=%#v", event.Preferences)
	}
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
	coreCalls      int
	streamCalls    map[string]int
	started        chan string
	status         protocol.Status
	preferences    protocol.TUIPreferences
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
	if f.status.Schema != "" {
		return f.status, nil
	}
	return protocol.Status{Schema: "mihari/v1", Revision: 3}, nil
}

func (f *fakeClient) Core(context.Context) (protocol.CoreStatus, error) {
	f.mu.Lock()
	f.coreCalls++
	f.mu.Unlock()
	return protocol.CoreStatus{Schema: "mihari/v1", Status: "running"}, nil
}

func (f *fakeClient) Subscriptions(context.Context) (protocol.SubscriptionList, error) {
	return protocol.SubscriptionList{Schema: "mihari/v1", Subscriptions: []protocol.Subscription{}}, nil
}

func (f *fakeClient) ProxyGroups(context.Context) (protocol.ProxyGroups, error) {
	return protocol.ProxyGroups{Schema: "mihari/v1", Groups: []protocol.ProxyGroup{}}, nil
}

func (f *fakeClient) Rules(context.Context) (protocol.RuleList, error) {
	return protocol.RuleList{Schema: "mihari/v1", Rules: []protocol.Rule{{Type: "MATCH", Proxy: "DIRECT"}}}, nil
}

func (f *fakeClient) RuleProviders(context.Context) (protocol.RuleProviderList, error) {
	return protocol.RuleProviderList{Schema: "mihari/v1", Providers: []protocol.RuleProvider{{Name: "OpenAI"}}}, nil
}

func (f *fakeClient) TUIPreferences(context.Context) (protocol.TUIPreferences, error) {
	return f.preferences, nil
}

func (f *fakeClient) WebGUI(context.Context) (protocol.WebGUIStatus, error) {
	return protocol.WebGUIStatus{Schema: "mihari/v1", GatewayAddr: "127.0.0.1:9191", GatewayHealth: "healthy"}, nil
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
