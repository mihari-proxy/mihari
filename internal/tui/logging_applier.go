package tui

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"sync"

	"github.com/mihari-proxy/mihari/internal/logging"
)

type localLogging interface {
	Apply(context.Context, logging.Config)
}

type loggingApplier interface {
	Submit(logging.Config) bool
	Cancel()
	CloseAndWait()
}

type ownedLoggingApplier struct {
	diagnostics ui.LocalTaskDiagnostics
	ctx         context.Context
	cancel      context.CancelFunc
	local       localLogging
	wake        chan struct{}
	done        chan struct{}

	mu         sync.Mutex
	closing    bool
	latest     logging.Config
	generation uint64
	closeOnce  sync.Once
}

func newLoggingApplier(parent context.Context, local localLogging) loggingApplier {
	return newLoggingApplierWithDiagnostics(parent, local, ui.LocalTaskDiagnostics{})
}

func newLoggingApplierWithDiagnostics(parent context.Context, local localLogging, diagnostics ui.LocalTaskDiagnostics) loggingApplier {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	applier := &ownedLoggingApplier{
		ctx: ctx, cancel: cancel, local: local, diagnostics: diagnostics,
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

func (a *ownedLoggingApplier) Cancel() {
	if a == nil {
		return
	}
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closing = true
		a.cancel()
		a.mu.Unlock()
	})
}

func (a *ownedLoggingApplier) CloseAndWait() {
	if a == nil {
		return
	}
	a.Cancel()
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
				// Each actual coalesced application has its own identity. Apply is
				// void: its logger-resource failures belong to FailureReporter.
				ctx := a.diagnostics.NewContext(a.ctx, "logging.apply")
				a.local.Apply(ctx, cfg)
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
