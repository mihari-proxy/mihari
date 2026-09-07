package logging

import (
	"context"
	"sync"
)

// recordMutex is a zero-value gate shared by records, rotations and snapshots.
// Waiting does not allocate a goroutine that could outlive its caller.
type recordMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *recordMutex) init() {
	m.once.Do(func() { m.token = make(chan struct{}, 1) })
}

func (m *recordMutex) Lock() {
	m.init()
	m.token <- struct{}{}
}

func (m *recordMutex) Unlock() { <-m.token }

func (m *recordMutex) lockContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.init()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	}
}
