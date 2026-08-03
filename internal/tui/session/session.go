package session

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

type EventKind string

const (
	EventStatus        EventKind = "status"
	EventCore          EventKind = "core"
	EventSubscriptions EventKind = "subscriptions"
	EventProxies       EventKind = "proxies"
	EventPreferences   EventKind = "preferences"
	EventRules         EventKind = "rules"
	EventRuleProviders EventKind = "rule-providers"
	EventTraffic       EventKind = "traffic"
	EventMemory        EventKind = "memory"
	EventLog           EventKind = "log"
	EventConnections   EventKind = "connections"
	EventReconnecting  EventKind = "reconnecting"
	EventConnected     EventKind = "connected"
	EventTerminalError EventKind = "terminal-error"
)

type Event struct {
	Kind          EventKind
	ObservedAt    time.Time
	Attempt       int
	Status        protocol.Status
	Core          protocol.CoreStatus
	Subscriptions protocol.SubscriptionList
	Proxies       protocol.ProxyGroups
	Preferences   protocol.TUIPreferences
	Rules         protocol.RuleList
	RuleProviders protocol.RuleProviderList
	Traffic       protocol.TrafficSample
	Memory        protocol.MemorySample
	Log           protocol.LogEntry
	Connections   protocol.ConnectionList
	Err           error
}

type Options struct {
	Backoff          func(attempt int) time.Duration
	OrderedQueueSize int
	EventBufferSize  int
}

type Session struct {
	client  Client
	options Options

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
	events  chan Event

	control chan Event
	ordered chan Event
	traffic chan Event
	memory  chan Event
}

func New(client Client, options Options) *Session {
	if options.Backoff == nil {
		options.Backoff = defaultBackoff
	}
	if options.OrderedQueueSize <= 0 {
		options.OrderedQueueSize = 256
	}
	if options.EventBufferSize <= 0 {
		options.EventBufferSize = 64
	}
	return &Session{
		client: client, options: options,
		done: make(chan struct{}), events: make(chan Event, options.EventBufferSize),
		control: make(chan Event, 16), ordered: make(chan Event, options.OrderedQueueSize),
		traffic: make(chan Event, 1), memory: make(chan Event, 1),
	}
}

func (s *Session) Start(parent context.Context) <-chan Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return s.events
	}
	s.started = true
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	go s.run(ctx)
	return s.events
}

func (s *Session) Close() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	cancel := s.cancel
	done := s.done
	s.mu.Unlock()
	cancel()
	<-done
}

func (s *Session) run(ctx context.Context) {
	var dispatch sync.WaitGroup
	dispatch.Add(1)
	go func() {
		defer dispatch.Done()
		s.dispatch(ctx)
	}()
	s.supervise(ctx)
	dispatch.Wait()
	close(s.events)
	close(s.done)
}

func (s *Session) supervise(ctx context.Context) {
	if s.client == nil {
		putOrdered(ctx, s.control, Event{Kind: EventTerminalError, Err: errors.New("control client is unavailable")})
		return
	}
	attempt := 0
	for ctx.Err() == nil {
		status, err := s.client.Status(ctx)
		if err != nil {
			attempt++
			if !putOrdered(ctx, s.control, Event{Kind: EventReconnecting, Attempt: attempt, Err: err}) || !waitBackoff(ctx, s.options.Backoff(attempt)) {
				return
			}
			continue
		}
		attempt = 0
		if !putOrdered(ctx, s.control, Event{Kind: EventStatus, Status: status}) {
			return
		}
		if slices.Contains(status.Capabilities, protocol.CapabilityCore) {
			coreStatus, coreErr := s.client.Core(ctx)
			if coreErr != nil {
				attempt++
				if !putOrdered(ctx, s.control, Event{Kind: EventReconnecting, Attempt: attempt, Err: coreErr}) || !waitBackoff(ctx, s.options.Backoff(attempt)) {
					return
				}
				continue
			}
			if !putOrdered(ctx, s.control, Event{Kind: EventCore, Core: coreStatus}) {
				return
			}
		}
		if slices.Contains(status.Capabilities, protocol.CapabilitySubscriptions) {
			subscriptions, subscriptionsErr := s.client.Subscriptions(ctx)
			if subscriptionsErr != nil {
				attempt++
				if !putOrdered(ctx, s.control, Event{Kind: EventReconnecting, Attempt: attempt, Err: subscriptionsErr}) || !waitBackoff(ctx, s.options.Backoff(attempt)) {
					return
				}
				continue
			}
			if !putOrdered(ctx, s.control, Event{Kind: EventSubscriptions, Subscriptions: subscriptions}) {
				return
			}
		}
		if slices.Contains(status.Capabilities, protocol.CapabilityProxies) {
			proxies, proxiesErr := s.client.ProxyGroups(ctx)
			if proxiesErr != nil {
				attempt++
				if !putOrdered(ctx, s.control, Event{Kind: EventReconnecting, Attempt: attempt, Err: proxiesErr}) || !waitBackoff(ctx, s.options.Backoff(attempt)) {
					return
				}
				continue
			}
			if !putOrdered(ctx, s.control, Event{Kind: EventProxies, Proxies: proxies}) {
				return
			}
		}
		if slices.Contains(status.Capabilities, protocol.CapabilityRules) {
			rules, rulesErr := s.client.Rules(ctx)
			if rulesErr != nil {
				attempt++
				if !putOrdered(ctx, s.control, Event{Kind: EventReconnecting, Attempt: attempt, Err: rulesErr}) || !waitBackoff(ctx, s.options.Backoff(attempt)) {
					return
				}
				continue
			}
			if !putOrdered(ctx, s.control, Event{Kind: EventRules, Rules: rules}) {
				return
			}
		}
		if slices.Contains(status.Capabilities, protocol.CapabilityRuleProviders) {
			providers, providersErr := s.client.RuleProviders(ctx)
			if providersErr != nil {
				attempt++
				if !putOrdered(ctx, s.control, Event{Kind: EventReconnecting, Attempt: attempt, Err: providersErr}) || !waitBackoff(ctx, s.options.Backoff(attempt)) {
					return
				}
				continue
			}
			if !putOrdered(ctx, s.control, Event{Kind: EventRuleProviders, RuleProviders: providers}) {
				return
			}
		}
		if slices.Contains(status.Capabilities, protocol.CapabilityPreferences) {
			preferences, preferencesErr := s.client.TUIPreferences(ctx)
			if preferencesErr != nil {
				attempt++
				if !putOrdered(ctx, s.control, Event{Kind: EventReconnecting, Attempt: attempt, Err: preferencesErr}) || !waitBackoff(ctx, s.options.Backoff(attempt)) {
					return
				}
				continue
			}
			if !putOrdered(ctx, s.control, Event{Kind: EventPreferences, Preferences: preferences}) {
				return
			}
		}
		if !putOrdered(ctx, s.control, Event{Kind: EventConnected}) {
			return
		}
		if err := s.runStreams(ctx); err == nil || ctx.Err() != nil {
			return
		} else {
			attempt++
			if !putOrdered(ctx, s.control, Event{Kind: EventReconnecting, Attempt: attempt, Err: err}) || !waitBackoff(ctx, s.options.Backoff(attempt)) {
				return
			}
		}
	}
}

func (s *Session) runStreams(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	errCh := make(chan error, 1)
	var streams sync.WaitGroup
	for _, kind := range []string{"traffic", "memory", "logs", "connections"} {
		kind := kind
		streams.Add(1)
		go func() {
			defer streams.Done()
			err := s.client.Stream(ctx, kind, func(source protocol.StreamEvent) error {
				event, decodeErr := decodeStreamEvent(source)
				if decodeErr != nil {
					return decodeErr
				}
				switch event.Kind {
				case EventTraffic:
					if !putLatest(ctx, s.traffic, event) {
						return ctx.Err()
					}
				case EventMemory:
					if !putLatest(ctx, s.memory, event) {
						return ctx.Err()
					}
				default:
					if !putOrdered(ctx, s.ordered, event) {
						return ctx.Err()
					}
				}
				return nil
			})
			if err == nil {
				err = errors.New("control stream ended")
			}
			select {
			case errCh <- err:
			default:
			}
		}()
	}
	var err error
	select {
	case <-parent.Done():
		err = parent.Err()
	case err = <-errCh:
	}
	cancel()
	streams.Wait()
	return err
}

func (s *Session) dispatch(ctx context.Context) {
	for {
		var event Event
		select {
		case <-ctx.Done():
			return
		case event = <-s.control:
		case event = <-s.ordered:
		case event = <-s.traffic:
		case event = <-s.memory:
		}
		select {
		case <-ctx.Done():
			return
		case s.events <- event:
		}
	}
}

func putOrdered(ctx context.Context, target chan<- Event, event Event) bool {
	select {
	case <-ctx.Done():
		return false
	case target <- event:
		return true
	}
}

func putLatest(ctx context.Context, target chan Event, event Event) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case target <- event:
			return true
		default:
		}
		select {
		case <-ctx.Done():
			return false
		case <-target:
		default:
		}
	}
}

func waitBackoff(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	duration := time.Second << min(attempt-1, 5)
	return min(duration, 30*time.Second)
}
