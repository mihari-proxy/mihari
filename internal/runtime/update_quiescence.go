package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"sync"
	"sync/atomic"
)

type applicationUpdateGate struct {
	mu       sync.Mutex
	owner    string
	epoch    uint64
	active   int
	idle     chan struct{}
	prepared atomic.Bool
}

type applicationWorkKey struct{}

func (m *Manager) beginApplicationWork(ctx context.Context) (context.Context, func(), error) {
	if m.ownsCoreUpdate(ctx) || ctx.Value(applicationWorkKey{}) == m {
		return ctx, func() {}, nil
	}
	finish, err := m.applicationUpdate.begin()
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, applicationWorkKey{}, m), finish, nil
}

func updatePreparationConflict() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "application update preparation is in progress"}
}

func (g *applicationUpdateGate) begin() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owner != "" {
		return nil, updatePreparationConflict()
	}
	if g.active == 0 {
		g.idle = make(chan struct{})
	}
	g.active++
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.active--
		if g.active == 0 {
			close(g.idle)
		}
	}, nil
}

// PrepareApplicationUpdate stops admitting mutations and drains accepted writes.
func (m *Manager) PrepareApplicationUpdate(ctx context.Context, owner string) error {
	if err := m.checkOpen(); err != nil {
		return err
	}
	if !m.businessMutationAllowed() {
		return updatePreparationConflict()
	}
	if owner == "" {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "missing update owner"}
	}
	g := &m.applicationUpdate
	g.mu.Lock()
	if g.owner != "" && g.owner != owner {
		g.mu.Unlock()
		return updatePreparationConflict()
	}
	if g.owner == "" {
		g.owner = owner
		g.epoch++
	}
	epoch := g.epoch
	if g.prepared.Load() {
		g.mu.Unlock()
		return nil
	}
	idle := g.idle
	active := g.active
	g.mu.Unlock()
	abandon := func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.owner == owner && g.epoch == epoch && !g.prepared.Load() {
			g.owner = ""
			g.epoch++
		}
	}
	if active > 0 {
		select {
		case <-ctx.Done():
			abandon()
			return ctx.Err()
		case <-idle:
		}
	}
	// Internal startup and observation writers retain maintenance ownership;
	// drain that critical section before publishing the prepared state.
	select {
	case <-ctx.Done():
		abandon()
		return ctx.Err()
	case <-m.maintenance:
	}
	defer m.releaseMutation()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owner != owner || g.epoch != epoch {
		return updatePreparationConflict()
	}
	if err := ctx.Err(); err != nil {
		g.owner = ""
		g.epoch++
		return err
	}
	g.prepared.Store(true)
	return nil
}

// ReleaseApplicationUpdate restores admission for the same preparation owner.
func (m *Manager) ReleaseApplicationUpdate(owner string) error {
	g := &m.applicationUpdate
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owner == "" {
		return nil
	}
	if g.owner != owner {
		return updatePreparationConflict()
	}
	g.prepared.Store(false)
	g.owner = ""
	g.epoch++
	return nil
}
