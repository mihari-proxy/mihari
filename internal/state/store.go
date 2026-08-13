package state

import (
	"sync/atomic"
	"time"
)

type Snapshot struct {
	Revision           uint64
	Version            string
	StartedAt          time.Time
	Health             string
	Core               CoreState
	Config             ConfigState
	ActiveSubscription string
	Subscriptions      []SubscriptionState
}

type ConfigState struct {
	Status           string
	DesiredRevision  uint64
	ObservedRevision uint64
	LastError        string
}

type SubscriptionState struct {
	ID          string
	Name        string
	Enabled     bool
	AutoRefresh bool
	Interval    string
	Cached      bool
	Generation  uint64
	UpdatedAt   time.Time
	LastError   string
}

type CoreState struct {
	Status      string
	Version     string
	Channel     string
	AlphaSHA    string
	PID         int
	Restarts    uint64
	LastError   string
	NextRetryAt time.Time
}

type Store struct {
	current atomic.Pointer[Snapshot]
}

func NewStore(initial Snapshot) *Store {
	store := &Store{}
	store.Store(initial)
	return store
}

func (s *Store) Load() Snapshot {
	return clone(*s.current.Load())
}

func (s *Store) Store(next Snapshot) {
	copy := clone(next)
	s.current.Store(&copy)
}

func clone(snapshot Snapshot) Snapshot {
	snapshot.Subscriptions = append([]SubscriptionState(nil), snapshot.Subscriptions...)
	return snapshot
}
