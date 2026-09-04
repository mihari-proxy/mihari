package state

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type CommandMeta struct {
	ID         string
	Source     string
	IfRevision *uint64
}

type Mutation func(Snapshot) (Snapshot, error)

// CommittedError marks a mutation whose snapshot must be stored even though the caller receives Err.
type CommittedError struct {
	Err error
}

func (e CommittedError) Error() string { return e.Err.Error() }

func (e CommittedError) Unwrap() error { return e.Err }

type Coordinator struct {
	mu    sync.Mutex
	store *Store
}

func NewCoordinator(store *Store) *Coordinator {
	return &Coordinator{store: store}
}

func (c *Coordinator) Do(ctx context.Context, meta CommandMeta, mutate Mutation) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.store.Load()
	if meta.IfRevision != nil && *meta.IfRevision != current.Revision {
		return Snapshot{}, protocol.APIError{
			Code:    protocol.CodeRevisionConflict,
			Message: "state revision changed",
			Details: map[string]any{
				"expected_revision": *meta.IfRevision,
				"current_revision":  current.Revision,
			},
		}
	}

	next, err := mutate(current)
	if err != nil {
		var committed CommittedError
		if errors.As(err, &committed) {
			next.Revision = current.Revision + 1
			c.store.Store(next)
			return next, fmt.Errorf("apply mutation: %w", committed.Err)
		}
		return Snapshot{}, fmt.Errorf("apply mutation: %w", err)
	}
	next.Revision = current.Revision + 1
	c.store.Store(next)
	return next, nil
}
