package tui

import (
	"context"
	"sync"

	"github.com/mihari-proxy/mihari/internal/logging"
)

type localLogging interface {
	Apply(context.Context, logging.Config)
}

type loggingApplier interface {
	Submit(logging.Config) bool
	CloseAndWait()
}

type ownedLoggingApplier struct {
	ctx    context.Context
	cancel context.CancelFunc
	local  localLogging
	wake   chan struct{}
	done   chan struct{}

	mu         sync.Mutex
	closing    bool
	latest     logging.Config
	generation uint64
	closeOnce  sync.Once
}

func newLoggingApplier(parent context.Context, local localLogging) loggingApplier {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	applier := &ownedLoggingApplier{
		ctx: ctx, cancel: cancel, local: local,
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
	go applier.run()
	return applier
}

func (a *ownedLoggingApplier) Submit(cfg logging.Config) bool {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return false
	}
	a.latest = cfg
	a.generation++
	a.mu.Unlock()
	select {
	case a.wake <- struct{}{}:
	default:
	}
	return true
}

func (a *ownedLoggingApplier) CloseAndWait() {
	if a == nil {
		return
	}
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closing = true
		a.cancel()
		a.mu.Unlock()
	})
	<-a.done
}

func (a *ownedLoggingApplier) run() {
	defer close(a.done)
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.wake:
		}
		for {
			a.drainWake()
			a.mu.Lock()
			if a.closing {
				a.mu.Unlock()
				return
			}
			cfg, generation := a.latest, a.generation
			a.mu.Unlock()

			if a.local != nil {
				a.local.Apply(a.ctx, cfg)
			}
			if a.ctx.Err() != nil {
				return
			}

			a.mu.Lock()
			changed := generation != a.generation
			a.mu.Unlock()
			if !changed {
				break
			}
		}
	}
}

func (a *ownedLoggingApplier) drainWake() {
	for {
		select {
		case <-a.wake:
		default:
			return
		}
	}
}
