package state

import (
	"sync/atomic"
	"time"
)

type Snapshot struct {
	Revision  uint64
	Version   string
	StartedAt time.Time
	Health    string
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
	return *s.current.Load()
}

func (s *Store) Store(next Snapshot) {
	copy := next
	s.current.Store(&copy)
}
