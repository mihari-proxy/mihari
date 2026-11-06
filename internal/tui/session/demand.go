package session

import (
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// Demand describes the visible page, independently of keyboard or modal focus.
type Demand struct {
	Page            string
	PreferencesOpen bool
}

var snapshotKinds = []EventKind{
	EventCore, EventSubscriptions, EventProxies, EventRules, EventRuleProviders,
	EventPreferences, EventWebGUI, EventLogging, EventRouting, EventDiagnostics,
	EventSystemProxy, EventTUN, EventEgress, EventOnboarding,
}
var coreSnapshotKinds = []EventKind{EventProxies, EventRules, EventRuleProviders, EventRouting, EventSystemProxy, EventTUN, EventEgress}

type refreshRequests struct {
	mu       sync.Mutex
	demand   Demand
	versions map[EventKind]uint64
	dirty    map[EventKind]bool
	fresh    map[EventKind]bool
	wake     chan struct{}
}

func newRefreshRequests() *refreshRequests {
	return &refreshRequests{demand: Demand{Page: "overview"}, versions: make(map[EventKind]uint64), dirty: make(map[EventKind]bool), fresh: make(map[EventKind]bool), wake: make(chan struct{}, 1)}
}
func demandInterval(d Demand, kind EventKind, base time.Duration) time.Duration {
	switch kind {
	case EventCore, EventLogging, EventDiagnostics:
		return base
	case EventSubscriptions:
		if d.Page == "overview" || d.Page == "subscriptions" {
			return base
		}
		return 5 * base
	case EventPreferences:
		if d.PreferencesOpen {
			return base
		}
		return 5 * base
	case EventProxies, EventRouting:
		if d.Page == "proxies" {
			return base
		}
	case EventRules, EventRuleProviders:
		if d.Page == "rules" {
			return base
		}
	case EventSystemProxy, EventTUN:
		if d.Page == "overview" || d.Page == "system" {
			return base
		}
	case EventEgress, EventOnboarding:
		if d.Page == "system" {
			return base
		}
	case EventWebGUI:
		if d.Page == "overview" || d.Page == "system" || d.Page == "web-gui" {
			return base
		}
	}
	return 0
}
func (r *refreshRequests) notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// SetDemand updates resource consumers. Requests without consumers are retired.
func (s *Session) SetDemand(next Demand) {
	r := s.refresh
	r.mu.Lock()
	old := r.demand
	if old == next {
		r.mu.Unlock()
		return
	}
	r.demand = next
	for _, kind := range snapshotKinds {
		before, after := demandInterval(old, kind, time.Second), demandInterval(next, kind, time.Second)
		if before > 0 && after == 0 {
			r.versions[kind]++
		}
		if after > 0 && (before == 0 || after < before) {
			r.dirty[kind] = true
			r.fresh[kind] = true
		}
	}
	r.mu.Unlock()
	r.notify()
}

// Refresh requests immediate reads and may share equivalent in-flight reads.
func (s *Session) Refresh(kinds ...EventKind) {
	r := s.refresh
	r.mu.Lock()
	for _, kind := range kinds {
		r.dirty[kind] = true
		r.fresh[kind] = true
	}
	r.mu.Unlock()
	r.notify()
}

// Invalidate retires observations started before an operation or owner change.
func (s *Session) Invalidate(kinds ...EventKind) {
	r := s.refresh
	r.mu.Lock()
	for _, kind := range kinds {
		r.versions[kind]++
		r.dirty[kind] = true
		r.fresh[kind] = true
	}
	r.mu.Unlock()
	r.notify()
}

// Valid rejects a queued result retired after it was fetched.
func (s *Session) Valid(event Event) bool {
	if event.Discarded {
		return false
	}
	if !event.Scheduled {
		return true
	}
	r := s.refresh
	r.mu.Lock()
	defer r.mu.Unlock()
	kind := event.Kind
	if kind == EventSnapshotLoading {
		kind = event.Resource
	}
	return event.Generation == r.versions[kind]
}
func snapshotCapability(kind EventKind) string {
	switch kind {
	case EventCore:
		return protocol.CapabilityCore
	case EventSubscriptions:
		return protocol.CapabilitySubscriptions
	case EventProxies:
		return protocol.CapabilityProxies
	case EventRules:
		return protocol.CapabilityRules
	case EventRuleProviders:
		return protocol.CapabilityRuleProviders
	case EventPreferences:
		return protocol.CapabilityPreferences
	case EventWebGUI:
		return protocol.CapabilityWebGUI
	case EventLogging:
		return protocol.CapabilityLogging
	case EventRouting:
		return protocol.CapabilityRouting
	case EventSystemProxy:
		return protocol.CapabilitySystemProxy
	case EventTUN:
		return protocol.CapabilityTUN
	case EventOnboarding:
		return protocol.CapabilityOnboarding
	case EventEgress:
		return protocol.CapabilityEgress
	}
	return ""
}

// SnapshotCapability identifies the existing daemon capability for a resource.
func SnapshotCapability(kind EventKind) string { return snapshotCapability(kind) }
