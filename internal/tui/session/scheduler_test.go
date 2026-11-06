package session

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestDemand_ResourceMatrixAndCadence(t *testing.T) {
	for _, tc := range []struct {
		page    string
		visible []EventKind
	}{
		{"overview", []EventKind{EventSystemProxy, EventTUN, EventWebGUI}},
		{"proxies", []EventKind{EventProxies, EventRouting}},
		{"rules", []EventKind{EventRules, EventRuleProviders}},
		{"connections", nil}, {"logs", nil}, {"subscriptions", nil},
		{"web-gui", []EventKind{EventWebGUI}},
		{"system", []EventKind{EventSystemProxy, EventTUN, EventEgress, EventWebGUI, EventOnboarding}},
		{"setup", nil},
	} {
		t.Run(tc.page, func(t *testing.T) {
			d := Demand{Page: tc.page}
			for _, kind := range snapshotKinds {
				want := time.Duration(0)
				switch kind {
				case EventCore, EventLogging, EventDiagnostics:
					want = time.Second
				case EventSubscriptions:
					want = 5 * time.Second
					if d.Page == "overview" || d.Page == "subscriptions" {
						want = time.Second
					}
				case EventPreferences:
					want = 5 * time.Second
				default:
					if slices.Contains(tc.visible, kind) {
						want = time.Second
					}
				}
				if got := demandInterval(d, kind, time.Second); got != want {
					t.Errorf("%s interval=%v want %v", kind, got, want)
				}
			}
			d.PreferencesOpen = true
			if got := demandInterval(d, EventPreferences, time.Second); got != time.Second {
				t.Fatal("F4 preference cadence did not change")
			}
		})
	}
}

type gatedRulesClient struct {
	*fakeClient
	calls    atomic.Int32
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (c *gatedRulesClient) Rules(ctx context.Context) (protocol.RuleList, error) {
	n := c.calls.Add(1)
	if n == 1 {
		close(c.entered)
		<-ctx.Done()
		close(c.canceled)
		// Model a native reader that cannot be interrupted immediately.
		<-c.release
		return protocol.RuleList{Rules: []protocol.Rule{{Payload: "old"}}}, nil
	}
	return protocol.RuleList{Rules: []protocol.Rule{{Payload: "new"}}}, nil
}
func scheduleFixture(t *testing.T, client Client, timeout time.Duration) (*Session, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	s := New(client, Options{PollInterval: time.Hour, SnapshotTimeout: timeout})
	s.SetDemand(Demand{Page: "rules"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.schedule(ctx) }()
	s.owners <- snapshotOwner{online: true, epoch: 1, status: protocol.Status{PID: 1, Capabilities: []string{protocol.CapabilityRules, protocol.CapabilitySubscriptions}}}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("scheduler leaked worker")
		}
	})
	return s, cancel, done
}
func scheduledEvent(t *testing.T, s *Session, kind EventKind, validOnly bool) Event {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-s.control:
			if event.Kind == kind && (!validOnly || s.Valid(event)) {
				return event
			}
		case <-timer.C:
			t.Fatalf("no %s event", kind)
			return Event{}
		}
	}
}
func TestSchedule_LeaveCancelsAndLateResultCannotOverwriteReturn(t *testing.T) {
	client := &gatedRulesClient{fakeClient: newFakeClient(), entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	s, _, _ := scheduleFixture(t, client, time.Second)
	defer func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
	}()
	<-client.entered
	// A global consumer remains responsive while the page reader is blocked.
	scheduledEvent(t, s, EventSubscriptions, true)
	s.SetDemand(Demand{Page: "overview"})
	select {
	case <-client.canceled:
	case <-time.After(time.Second):
		t.Fatal("unused read not canceled")
	}
	s.SetDemand(Demand{Page: "rules"})
	close(client.release)
	old := scheduledEvent(t, s, EventRules, false)
	if !old.Discarded || s.Valid(old) {
		t.Fatal("late old-page result accepted")
	}
	next := scheduledEvent(t, s, EventRules, true)
	if next.Rules.Rules[0].Payload != "new" || client.calls.Load() != 2 {
		t.Fatal("return did not read current data exactly once")
	}
}
func TestSchedule_OperationInvalidationRequiresPostOperationRead(t *testing.T) {
	client := &gatedRulesClient{fakeClient: newFakeClient(), entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	s, _, _ := scheduleFixture(t, client, time.Second)
	defer func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
	}()
	<-client.entered
	for range 10 {
		s.Refresh(EventRules)
	}
	s.Invalidate(EventRules)
	select {
	case <-client.canceled:
	case <-time.After(time.Second):
		t.Fatal("pre-operation read not retired")
	}
	// Coalesced invalidations still need only one replacement execution.
	for range 10 {
		s.Invalidate(EventRules)
	}
	close(client.release)
	event := scheduledEvent(t, s, EventRules, true)
	if event.Rules.Rules[0].Payload != "new" || client.calls.Load() != 2 {
		t.Fatalf("calls=%d event=%+v", client.calls.Load(), event)
	}
	s.Invalidate(EventRules)
	if s.Valid(event) {
		t.Fatal("queued pre-operation result remained valid")
	}
}

type deadlineRulesClient struct{ *fakeClient }

func (c deadlineRulesClient) Rules(ctx context.Context) (protocol.RuleList, error) {
	<-ctx.Done()
	return protocol.RuleList{}, ctx.Err()
}
func TestSchedule_RequestDeadlineDoesNotBlockOtherResources(t *testing.T) {
	s, _, _ := scheduleFixture(t, deadlineRulesClient{newFakeClient()}, 20*time.Millisecond)
	scheduledEvent(t, s, EventSubscriptions, true)
	event := scheduledEvent(t, s, EventRules, true)
	if !errors.Is(event.Err, context.DeadlineExceeded) {
		t.Fatalf("deadline lost: %v", event.Err)
	}
}
func TestSchedule_CloseJoinsRetiredNativeReader(t *testing.T) {
	client := &gatedRulesClient{fakeClient: newFakeClient(), entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	_, cancel, done := scheduleFixture(t, client, time.Second)
	defer func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
	}()
	<-client.entered
	cancel()
	<-client.canceled
	select {
	case <-done:
		t.Fatal("owner exited with a live native worker")
	default:
	}
	close(client.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("owner did not join completed worker")
	}
}

type manualSnapshotClock struct {
	mu    sync.Mutex
	now   time.Time
	timer *manualSnapshotTimer
}
type manualSnapshotTimer struct {
	ticks  chan time.Time
	resets chan time.Duration
}

func (c *manualSnapshotClock) Now() time.Time            { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *manualSnapshotClock) NewTimer() snapshotTimer   { return c.timer }
func (t *manualSnapshotTimer) Channel() <-chan time.Time { return t.ticks }
func (t *manualSnapshotTimer) Stop()                     {}
func (t *manualSnapshotTimer) Reset(d time.Duration)     { t.resets <- d }
func TestSchedule_PeriodicDeadlineUsesItsOwnClockPhase(t *testing.T) {
	timer := &manualSnapshotTimer{ticks: make(chan time.Time, 1), resets: make(chan time.Duration, 64)}
	clock := &manualSnapshotClock{now: time.Unix(100, 1), timer: timer}
	s := New(newFakeClient(), Options{snapshotClock: clock})
	s.SetDemand(Demand{Page: "rules"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.schedule(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	s.owners <- snapshotOwner{online: true, epoch: 1, status: protocol.Status{Capabilities: []string{protocol.CapabilityRules}}}
	scheduledEvent(t, s, EventRules, true)
	for range 4 {
		var delay time.Duration
		select {
		case delay = <-timer.resets:
		case <-time.After(time.Second):
			t.Fatal("resource did not schedule its next deadline")
		}
		if delay != time.Second {
			t.Fatalf("next delay=%v", delay)
		}
		clock.mu.Lock()
		clock.now = clock.now.Add(delay)
		now := clock.now
		clock.mu.Unlock()
		timer.ticks <- now
		scheduledEvent(t, s, EventRules, true)
	}
}

func TestSchedule_F4CadenceChangesWithoutAnExtraFastPollAfterClose(t *testing.T) {
	timer := &manualSnapshotTimer{ticks: make(chan time.Time, 1), resets: make(chan time.Duration, 64)}
	clock := &manualSnapshotClock{now: time.Unix(100, 1), timer: timer}
	s := New(newFakeClient(), Options{snapshotClock: clock})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.schedule(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	s.owners <- snapshotOwner{online: true, epoch: 1, status: protocol.Status{Capabilities: []string{protocol.CapabilityPreferences}}}
	scheduledEvent(t, s, EventPreferences, true)
	expectDelay := func(want time.Duration) {
		t.Helper()
		select {
		case got := <-timer.resets:
			if got != want {
				t.Fatalf("next delay=%v want %v", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("scheduler did not arm resource deadline")
		}
	}
	expectDelay(5 * time.Second)
	s.SetDemand(Demand{Page: "rules", PreferencesOpen: true})
	scheduledEvent(t, s, EventPreferences, true)
	expectDelay(time.Second)
	s.SetDemand(Demand{Page: "rules"})
	expectDelay(5 * time.Second)
	clock.mu.Lock()
	clock.now = clock.now.Add(5 * time.Second)
	now := clock.now
	clock.mu.Unlock()
	timer.ticks <- now
	scheduledEvent(t, s, EventPreferences, true)
	expectDelay(5 * time.Second)
}

func TestSchedule_QueuedLoadingStateIsRetiredWithItsRequest(t *testing.T) {
	s := New(newFakeClient(), Options{})
	s.SetDemand(Demand{Page: "rules"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.schedule(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	s.owners <- snapshotOwner{online: true, epoch: 1, status: protocol.Status{Capabilities: []string{protocol.CapabilityRules}}}
	loading := scheduledEvent(t, s, EventSnapshotLoading, false)
	if loading.Resource != EventRules {
		t.Fatal("unexpected fixture resource")
	}
	s.Invalidate(EventRules)
	if s.Valid(loading) {
		t.Fatal("queued loading marker survived its retired request")
	}
}
