package session

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/observation"
)

type snapshotOwner struct {
	status protocol.Status
	epoch  uint64
	online bool
}
type snapshotFlight struct {
	generation uint64
	cancel     context.CancelFunc
}
type snapshotResult struct {
	kind                  EventKind
	generation            uint64
	epoch                 uint64
	events                []Event
	diagnosticInstance    string
	diagnosticSequence    uint64
	diagnosticUnsupported bool
}
type scheduledResource struct {
	flight  *snapshotFlight
	last    time.Time
	started bool
}

func (s *Session) snapshotLimit() time.Duration {
	if s.options.SnapshotTimeout > 0 {
		return s.options.SnapshotTimeout
	}
	return 5 * time.Second
}
func (s *Session) readStatus(ctx context.Context) (protocol.Status, error) {
	request, cancel := context.WithTimeout(ctx, s.snapshotLimit())
	defer cancel()
	return s.client.Status(request)
}

// schedule owns resource workers; Session joins it before closing events/logger.
func (s *Session) schedule(ctx context.Context) {
	resources := make(map[EventKind]*scheduledResource)
	for _, kind := range snapshotKinds {
		resources[kind] = &scheduledResource{}
	}
	completed := make(chan snapshotResult, len(snapshotKinds))
	owner := snapshotOwner{}
	var core protocol.CoreStatus
	coreKnown := false
	var loggingRevision *uint64
	clock := s.options.snapshotClock
	if clock == nil {
		clock = wallSnapshotClock{}
	}
	timer := clock.NewTimer()
	defer timer.Stop()
	now := clock.Now()
	retire := func(kinds []EventKind) {
		s.Invalidate(kinds...)
		for _, kind := range kinds {
			resources[kind].last = time.Time{}
			if f := resources[kind].flight; f != nil {
				f.cancel()
			}
		}
	}
	start := func() {
		if ctx.Err() != nil {
			return
		}
		r := s.refresh
		for _, kind := range snapshotKinds {
			if ctx.Err() != nil {
				return
			}
			resource := resources[kind]
			r.mu.Lock()
			interval := demandInterval(r.demand, kind, s.options.PollInterval)
			version := r.versions[kind]
			dirty, fresh := r.dirty[kind], r.fresh[kind]
			supported := s.snapshotSupported(kind, owner.status)
			wanted := owner.online && !owner.status.StartupNetwork.Applying() && interval > 0 && supported
			if resource.flight != nil {
				if !wanted || resource.flight.generation != version {
					resource.flight.cancel()
				}
				// Ordinary ticks and refreshes join the existing execution.
				if wanted && resource.flight.generation == version {
					delete(r.dirty, kind)
					delete(r.fresh, kind)
				}
				r.mu.Unlock()
				continue
			}
			if !wanted {
				r.mu.Unlock()
				continue
			}
			if !dirty && now.Before(resource.last.Add(interval)) {
				r.mu.Unlock()
				continue
			}
			if kind == EventLogging && loggingRevision != nil && *loggingRevision >= owner.status.Revision && !dirty {
				r.mu.Unlock()
				continue
			}
			delete(r.dirty, kind)
			delete(r.fresh, kind)
			r.mu.Unlock()
			request, cancel := context.WithTimeout(ctx, s.snapshotLimit())
			if fresh {
				request = observation.WithFresh(request)
			}
			resource.flight = &snapshotFlight{generation: version, cancel: cancel}
			resource.last = now
			captured := owner
			// Private reader state keeps legacy endpoint parsing and diagnostics
			// collection on the worker, without sharing mutable cursors.
			buffer := 1
			if kind == EventDiagnostics {
				buffer = 3
			}
			reader := &Session{client: s.client, control: make(chan Event, buffer), loggingEpoch: captured.epoch,
				loggingCapability: true, diagnosticInstance: s.diagnosticInstance,
				diagnosticSequence: s.diagnosticSequence, diagnosticUnsupported: s.diagnosticUnsupported}
			if kind != EventDiagnostics && (!resource.started || fresh) {
				putOrdered(ctx, s.control, Event{Kind: EventSnapshotLoading, Resource: kind, Generation: version, Epoch: captured.epoch, Status: captured.status, Scheduled: true})
			}
			resource.started = true
			go func(kind EventKind, version uint64, reader *Session) {
				defer cancel()
				events := reader.fetchResource(request, kind, captured.status)
				for i := range events {
					if diagnostics.NormalCancellation(request, events[i].Err) {
						events[i].Err = nil
					}
				}
				result := snapshotResult{kind: kind, generation: version, epoch: captured.epoch, events: events,
					diagnosticInstance: reader.diagnosticInstance, diagnosticSequence: reader.diagnosticSequence, diagnosticUnsupported: reader.diagnosticUnsupported}
				// The bounded completion slot belongs to this worker even on
				// shutdown; its owner drains and joins every resource below.
				completed <- result
			}(kind, version, reader)
		}
	}
	scheduleNext := func() {
		timer.Stop()
		delay := time.Duration(-1)
		r := s.refresh
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, kind := range snapshotKinds {
			resource := resources[kind]
			interval := demandInterval(r.demand, kind, s.options.PollInterval)
			supported := s.snapshotSupported(kind, owner.status)
			if !owner.online || owner.status.StartupNetwork.Applying() || interval == 0 || !supported || resource.flight != nil {
				continue
			}
			dirty := r.dirty[kind]
			if kind == EventLogging && loggingRevision != nil && *loggingRevision >= owner.status.Revision && !dirty {
				continue
			}
			next := resource.last.Add(interval).Sub(now)
			if dirty || next < 0 {
				next = 0
			}
			if delay < 0 || next < delay {
				delay = next
			}
		}
		if delay >= 0 {
			timer.Reset(delay)
		}
	}
	defer func() {
		pending := 0
		for _, resource := range resources {
			if resource.flight != nil {
				resource.flight.cancel()
				pending++
			}
		}
		for range pending {
			<-completed
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case next := <-s.owners:
			changed := owner.epoch != next.epoch || owner.status.PID != next.status.PID || !owner.status.StartedAt.Equal(next.status.StartedAt)
			resumed := !owner.online && next.online
			if changed || resumed {
				retire(snapshotKinds)
				loggingRevision = nil
				coreKnown = false
			}
			if !next.online {
				retire(snapshotKinds)
				loggingRevision = nil
			} else if !slices.Equal(owner.status.Capabilities, next.status.Capabilities) {
				retire(snapshotKinds)
				loggingRevision = nil
			}
			owner = next
		case <-s.refresh.wake:
		case <-timer.Channel():
		case result := <-completed:
			resource := resources[result.kind]
			resource.flight = nil
			valid := owner.online && result.epoch == owner.epoch
			r := s.refresh
			r.mu.Lock()
			valid = valid && result.generation == r.versions[result.kind]
			r.mu.Unlock()
			if valid && result.kind == EventCore && len(result.events) > 0 && result.events[0].Err == nil {
				next := result.events[0].Core
				changed := !coreKnown || core.PID != next.PID || !core.StartedAt.Equal(next.StartedAt) || core.Restarts != next.Restarts
				core, coreKnown = next, true
				if changed {
					retire(coreSnapshotKinds)
				}
			}
			for _, event := range result.events {
				event.Scheduled = true
				event.Generation = result.generation
				event.Epoch = result.epoch
				event.Discarded = !valid
				if !putOrdered(ctx, s.control, event) {
					return
				}
				if valid && event.Err == nil && event.Kind == EventLogging {
					revision := event.Logging.Revision
					loggingRevision = &revision
				}
			}
			if valid && result.kind == EventDiagnostics {
				s.diagnosticInstance = result.diagnosticInstance
				s.diagnosticSequence = result.diagnosticSequence
				s.diagnosticUnsupported = result.diagnosticUnsupported
			}
		}
		now = clock.Now()
		start()
		scheduleNext()
	}
}

func (s *Session) fetchResource(ctx context.Context, kind EventKind, status protocol.Status) []Event {
	var err error
	switch kind {
	case EventLogging:
		s.pollLogging(ctx, status)
	case EventRouting:
		s.pollRouting(ctx, status)
	case EventDiagnostics:
		s.pollDiagnostics(ctx, status)
	case EventOnboarding:
		var value protocol.OnboardingStatus
		if client, ok := s.client.(interface {
			Onboarding(context.Context) (protocol.OnboardingStatus, error)
		}); ok {
			value, err = client.Onboarding(ctx)
		} else {
			err = errors.New("onboarding query unavailable")
		}
		s.control <- Event{Kind: kind, Onboarding: value, Err: err}
	case EventSystemProxy:
		var value protocol.SystemProxyStatus
		if client, ok := s.client.(interface {
			SystemProxy(context.Context) (protocol.SystemProxyStatus, error)
		}); ok {
			value, err = client.SystemProxy(ctx)
		} else {
			err = errors.New("system proxy query unavailable")
		}
		s.control <- Event{Kind: kind, SystemProxy: value, Err: err}
	case EventTUN:
		var value protocol.TunStatus
		if client, ok := s.client.(interface {
			Tun(context.Context) (protocol.TunStatus, error)
		}); ok {
			value, err = client.Tun(ctx)
		} else {
			err = errors.New("TUN query unavailable")
		}
		s.control <- Event{Kind: kind, TUN: value, Err: err}
	case EventEgress:
		var value protocol.EgressStatus
		if client, ok := s.client.(interface {
			Egress(context.Context) (protocol.EgressStatus, error)
		}); ok {
			value, err = client.Egress(ctx)
		} else {
			err = errors.New("egress query unavailable")
		}
		s.control <- Event{Kind: kind, Egress: value, Err: err}
	default:
		selected := status
		selected.Capabilities = []string{snapshotCapability(kind)}
		err = s.pollSnapshots(ctx, selected)
	}
	events := make([]Event, 0, len(s.control)+1)
	for len(s.control) > 0 {
		event := <-s.control
		event.Status = status
		events = append(events, event)
	}
	if ctx.Err() != nil && (len(events) == 0 || kind == EventDiagnostics) {
		events = append(events, Event{Kind: kind, Status: status, Err: ctx.Err(), DiagnosticQuery: kind == EventDiagnostics})
	} else if err != nil && len(events) == 0 {
		events = append(events, Event{Kind: kind, Status: status, Err: err})
	}
	return events
}

func (s *Session) snapshotSupported(kind EventKind, status protocol.Status) bool {
	if kind == EventDiagnostics {
		_, ok := s.client.(interface {
			Diagnostics(context.Context, string, uint64, int) (protocol.DiagnosticList, error)
		})
		return ok && (slices.Contains(status.Capabilities, protocol.CapabilityDiagnostics) || !s.diagnosticUnsupported)
	}
	return slices.Contains(status.Capabilities, snapshotCapability(kind))
}
