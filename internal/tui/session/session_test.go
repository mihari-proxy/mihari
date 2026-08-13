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

func TestSession_SnapshotFailureDoesNotReportDaemonReconnect(t *testing.T) {
	fake := newFakeClient()
	fake.status = protocol.Status{Schema: "mihari/v1", Capabilities: []string{protocol.CapabilityCore}}
	fake.failNextCore()
	session := New(fake, Options{Backoff: func(int) time.Duration { return 0 }, PollInterval: 20 * time.Millisecond})
	events := session.Start(context.Background())
	defer session.Close()

	deadline := time.After(3 * time.Second)
	connected := false
	for {
		select {
		case event, open := <-events:
			if !open {
				t.Fatal("events closed before snapshot recovery")
			}
			if event.Kind == EventReconnecting {
				t.Fatalf("snapshot failure was promoted to daemon reconnect: %v", event.Err)
			}
			if event.Kind == EventConnected {
				connected = true
			}
			if connected && event.Kind == EventCore {
				return
			}
		case <-deadline:
			t.Fatal("core snapshot was not retried")
		}
	}
}

func TestSession_CoreRestartReconnectsStreamsWithoutDaemonReconnect(t *testing.T) {
	for _, test := range []struct {
		name string
		from string
		to   string
	}{
		{name: "stable to alpha", from: "stable", to: "alpha"},
		{name: "alpha to stable", from: "alpha", to: "stable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeClient()
			fake.emitOnReconnect = true
			fake.status = protocol.Status{Schema: "mihari/v1", Capabilities: []string{protocol.CapabilityCore}}
			fake.core = protocol.CoreStatus{Schema: "mihari/v1", Status: "running", Channel: test.from}
			session := New(fake, Options{Backoff: func(int) time.Duration { return 0 }, PollInterval: 20 * time.Millisecond})
			events := session.Start(context.Background())
			defer session.Close()

			initial := waitForEvent(t, events, EventCore)
			if initial.Core.Channel != test.from {
				t.Fatalf("initial channel=%q want %q", initial.Core.Channel, test.from)
			}
			waitForEvent(t, events, EventConnected)
			waitForStreamStarts(t, fake.started, 4)

			fake.setCoreChannel(test.to)
			fake.failNextCore()
			close(fake.streamBreak)

			restarted := make(map[string]bool)
			delivered := make(map[EventKind]bool)
			observedTarget := false
			deadline := time.After(3 * time.Second)
			for len(restarted) < 4 || len(delivered) < 4 || !observedTarget {
				select {
				case event, open := <-events:
					if !open {
						t.Fatal("events closed while streams were reconnecting")
					}
					if event.Kind == EventReconnecting {
						t.Fatalf("controller stream interruption was promoted to daemon reconnect: %v", event.Err)
					}
					if event.Kind == EventCore && event.Core.Channel == test.to {
						observedTarget = true
					}
					switch event.Kind {
					case EventTraffic, EventMemory, EventLog, EventConnections:
						delivered[event.Kind] = true
					}
				case kind := <-fake.started:
					if fake.streamCallCount(kind) >= 2 {
						restarted[kind] = true
					}
				case <-deadline:
					t.Fatalf("streams=%v delivered=%v target channel observed=%v calls=%v", restarted, delivered, observedTarget, fake.streamCallCounts())
				}
			}
		})
	}
}

func TestSession_StreamFailureReportsReconnectWhenDaemonStatusFails(t *testing.T) {
	fake := newFakeClient()
	session := New(fake, Options{Backoff: func(int) time.Duration { return 0 }, PollInterval: time.Hour})
	events := session.Start(context.Background())
	defer session.Close()
	waitForEvent(t, events, EventConnected)
	waitForStreamStarts(t, fake.started, 4)

	fake.failNextStatus()
	close(fake.streamBreak)
	waitForEvent(t, events, EventReconnecting)
}

func TestSession_ConsecutiveStreamFailuresIncreaseBackoffUntilStreamDataRecovers(t *testing.T) {
	fake := newFakeClient()
	attempts := make(chan int, 4)
	session := New(fake, Options{
		Backoff: func(attempt int) time.Duration {
			attempts <- attempt
			return 0
		},
		PollInterval: 10 * time.Millisecond,
	})
	events := session.Start(context.Background())
	defer session.Close()
	waitForEvent(t, events, EventConnected)
	waitForStreamStarts(t, fake.started, 4)

	close(fake.streamBreak)
	if attempt := waitForAttempt(t, attempts); attempt != 1 {
		t.Fatalf("first stream retry attempt=%d want 1", attempt)
	}
	waitForStreamStarts(t, fake.started, 4)
	statusCalls := fake.statusCallCount()
	waitForStatusCallAfter(t, fake, statusCalls)

	close(fake.secondStreamBreak)
	if attempt := waitForAttempt(t, attempts); attempt != 2 {
		t.Fatalf("second stream retry attempt=%d want 2", attempt)
	}
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

func waitForAttempt(t *testing.T, attempts <-chan int) int {
	t.Helper()
	select {
	case attempt := <-attempts:
		return attempt
	case <-time.After(3 * time.Second):
		t.Fatal("stream retry did not request backoff")
		return 0
	}
}

func waitForStreamStarts(t *testing.T, started <-chan string, count int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for range count {
		select {
		case <-started:
		case <-deadline:
			t.Fatalf("received fewer than %d stream starts", count)
		}
	}
}

func waitForStatusCallAfter(t *testing.T, fake *fakeClient, previous int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for fake.statusCallCount() <= previous {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatalf("status calls did not advance beyond %d", previous)
		}
	}
}

type fakeClient struct {
	mu                sync.Mutex
	statusFailures    int
	statusCalls       int
	coreFailures      int
	coreCalls         int
	streamCalls       map[string]int
	started           chan string
	streamBreak       chan struct{}
	secondStreamBreak chan struct{}
	emitOnReconnect   bool
	status            protocol.Status
	core              protocol.CoreStatus
	preferences       protocol.TUIPreferences
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		streamCalls:       make(map[string]int),
		started:           make(chan string, 16),
		streamBreak:       make(chan struct{}),
		secondStreamBreak: make(chan struct{}),
	}
}

func (f *fakeClient) Status(context.Context) (protocol.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
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
	defer f.mu.Unlock()
	f.coreCalls++
	if f.coreFailures > 0 {
		f.coreFailures--
		return protocol.CoreStatus{}, errors.New("mihomo controller unavailable")
	}
	if f.core.Schema != "" {
		return f.core, nil
	}
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

func (f *fakeClient) Stream(ctx context.Context, kind string, receive func(protocol.StreamEvent) error) error {
	f.mu.Lock()
	f.streamCalls[kind]++
	call := f.streamCalls[kind]
	streamBreak := f.streamBreak
	secondStreamBreak := f.secondStreamBreak
	emitOnReconnect := f.emitOnReconnect
	f.mu.Unlock()
	f.started <- kind
	if call == 1 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-streamBreak:
			return errors.New("mihomo controller unavailable")
		}
	}
	if call == 2 {
		if emitOnReconnect {
			if err := receive(protocol.StreamEvent{Schema: "mihari/v1", Stream: kind, Data: []byte("{}")}); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-secondStreamBreak:
			return errors.New("mihomo controller unavailable")
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeClient) streamCallCount(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streamCalls[kind]
}

func (f *fakeClient) streamCallCounts() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := make(map[string]int, len(f.streamCalls))
	for kind, count := range f.streamCalls {
		counts[kind] = count
	}
	return counts
}

func (f *fakeClient) statusCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusCalls
}

func (f *fakeClient) setCoreChannel(channel string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.core.Channel = channel
}

func (f *fakeClient) failNextStatus() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusFailures++
}

func (f *fakeClient) failNextCore() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.coreFailures++
}
