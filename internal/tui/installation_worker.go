package tui

import (
	"context"
	"sync"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// Run owns all inspection/preparation calls, including discarded tea messages.
type installationWorker struct {
	mu      sync.Mutex
	closing bool
	next    uint64
	cancels map[uint64]context.CancelFunc
	workers sync.WaitGroup
}

func newInstallationWorker(actions InstallationActions) (*installationWorker, InstallationActions) {
	w := &installationWorker{cancels: map[uint64]context.CancelFunc{}}
	if inspect := actions.Inspect; inspect != nil {
		actions.Inspect = func(ctx context.Context) (status protocol.InstallationStatus, err error) {
			err = w.run(ctx, func(ctx context.Context) error { status, err = inspect(ctx); return err })
			return status, err
		}
	}
	if plan := actions.Plan; plan != nil {
		actions.Plan = func(ctx context.Context, request app.InstallationPlanRequest) (result app.InstallationPlan, err error) {
			err = w.run(ctx, func(ctx context.Context) error { result, err = plan(ctx, request); return err })
			return result, err
		}
	}
	return w, actions
}

func (w *installationWorker) run(ctx context.Context, call func(context.Context) error) error {
	child, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	if w.closing {
		w.mu.Unlock()
		cancel()
		return context.Canceled
	}
	w.next++
	id := w.next
	w.cancels[id] = cancel
	w.workers.Add(1)
	w.mu.Unlock()
	defer func() { cancel(); w.mu.Lock(); delete(w.cancels, id); w.mu.Unlock(); w.workers.Done() }()
	return call(child)
}

func (w *installationWorker) shutdown() {
	w.mu.Lock()
	w.closing = true
	for _, cancel := range w.cancels {
		cancel()
	}
	w.mu.Unlock()
	w.workers.Wait()
}
