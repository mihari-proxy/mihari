package tui

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"sync"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// Run owns all inspection/preparation calls, including discarded tea messages.
type installationWorker struct {
	diagnostics ui.LocalTaskDiagnostics
	mu          sync.Mutex
	closing     bool
	next        uint64
	cancels     map[uint64]context.CancelFunc
	workers     sync.WaitGroup
}

func newInstallationWorker(actions InstallationActions) (*installationWorker, InstallationActions) {
	w := &installationWorker{diagnostics: actions.Diagnostics, cancels: map[uint64]context.CancelFunc{}}
	if inspect := actions.Inspect; inspect != nil {
		actions.Inspect = func(ctx context.Context) (status protocol.InstallationStatus, err error) {
			err = w.run(ctx, "installation.inspect", func(ctx context.Context) error { status, err = inspect(ctx); return err })
			return status, err
		}
	}
	if plan := actions.Plan; plan != nil {
		actions.Plan = func(ctx context.Context, request app.InstallationPlanRequest) (result app.InstallationPlan, err error) {
			err = w.run(ctx, "installation.plan", func(ctx context.Context) error { result, err = plan(ctx, request); return err })
			return result, err
		}
	}
	return w, actions
}

func (w *installationWorker) run(ctx context.Context, name string, call func(context.Context) error) error {
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
	child = w.diagnostics.Context(child, name)
	return w.diagnostics.ReportFailure(child, name+".failed", call(child))
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
