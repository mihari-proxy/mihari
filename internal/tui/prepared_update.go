package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
	"io"
	"sync"
)

// runPreparedUpdater joins even a tea.Cmd whose result is discarded on quit.
// Candidate ownership stays here until Run has applied or abandoned the result.
type runPreparedUpdater struct {
	systempage.PreparedSelfUpdater
	diagnostics ui.LocalTaskDiagnostics
	next        uint64
	cancels     map[uint64]context.CancelFunc
	mu          sync.Mutex
	closing     bool
	workers     sync.WaitGroup
	candidates  []update.PreparedUpdate
}

func newRunPreparedUpdater(u systempage.PreparedSelfUpdater) *runPreparedUpdater {
	return &runPreparedUpdater{PreparedSelfUpdater: u, cancels: map[uint64]context.CancelFunc{}}
}

// liveUpdater borrows the reporter only for requests made before TUI cleanup.
// ApplyPrepared still uses the original updater after the logger has closed.
func (w *runPreparedUpdater) liveUpdater() systempage.PreparedSelfUpdater {
	switch u := w.PreparedSelfUpdater.(type) {
	case update.SelfUpdater:
		u.Reporter = w.diagnostics.Reporter
		return u
	case *update.SelfUpdater:
		if u != nil {
			copy := *u
			copy.Reporter = w.diagnostics.Reporter
			return copy
		}
	}
	return w.PreparedSelfUpdater
}

func (w *runPreparedUpdater) Check(ctx context.Context, current, channel string) (update.CheckResult, error) {
	child, updater, finish, err := w.begin(ctx, "self.check")
	if err != nil {
		return update.CheckResult{}, err
	}
	defer finish()
	result, err := updater.Check(child, current, channel)
	if diagnostics.NormalCancellation(child, err) {
		return result, err
	}
	return result, w.diagnostics.ReportFailure(child, "self.check.failed", err)
}

func (w *runPreparedUpdater) Prepare(ctx context.Context, binary, current, channel string) (update.PreparedUpdate, error) {
	child, updater, finish, err := w.begin(ctx, "self.prepare")
	if err != nil {
		return update.PreparedUpdate{}, err
	}
	defer finish()
	prepared, err := updater.Prepare(child, binary, current, channel)
	w.mu.Lock()
	w.candidates = append(w.candidates, prepared)
	w.mu.Unlock()
	return prepared, w.diagnostics.ReportFailure(child, "self.prepare.failed", err)
}

func (w *runPreparedUpdater) begin(ctx context.Context, operation string) (context.Context, systempage.PreparedSelfUpdater, func(), error) {
	child, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	if w.closing {
		w.mu.Unlock()
		cancel()
		return nil, nil, nil, context.Canceled
	}
	w.next++
	id := w.next
	w.cancels[id] = cancel
	w.workers.Add(1)
	updater := w.liveUpdater()
	w.mu.Unlock()
	finish := func() {
		cancel()
		w.mu.Lock()
		delete(w.cancels, id)
		w.mu.Unlock()
		w.workers.Done()
	}
	return w.diagnostics.Context(child, operation), updater, finish, nil
}
func (w *runPreparedUpdater) shutdown() {
	w.mu.Lock()
	w.closing = true
	for _, cancel := range w.cancels {
		cancel()
	}
	w.mu.Unlock()
	w.workers.Wait()
}
func (w *runPreparedUpdater) close() (err error) {
	w.shutdown()
	w.mu.Lock()
	candidates := w.candidates
	w.candidates = nil
	w.mu.Unlock()
	for i := range candidates {
		err = errors.Join(err, candidates[i].Close())
	}
	return err
}
func finishPreparedRun(ctx context.Context, final tea.Model, runErr error, out io.Writer, relaunch func() error, cleanup func(tea.Model) error, apply func(context.Context, update.PreparedUpdate) (update.Result, error)) error {
	var model Model
	switch value := final.(type) {
	case Model:
		model = value
	case *Model:
		if value != nil {
			model = *value
		}
	}
	if model.preparedUpdate == nil {
		return finishRun(final, runErr, out, relaunch, cleanup)
	}
	var cleanupErr error
	if cleanup != nil {
		cleanupErr = cleanup(final)
	}
	if runErr != nil || cleanupErr != nil {
		return errors.Join(runErr, cleanupErr)
	}
	if apply == nil {
		return fmt.Errorf("apply prepared Mihari update: unavailable")
	}
	// Cleanup deliberately closes logging before replacement. This task only
	// binds metadata; its existing safe error/partial-success outlet remains owner.
	ctx = (ui.LocalTaskDiagnostics{}).Context(ctx, "self.apply")
	result, err := apply(ctx, *model.preparedUpdate)
	if !result.Updated {
		if err != nil {
			return errors.Join(err, fmt.Errorf("mihari update did not complete; reopen Mihari and retry"))
		}
		return nil
	}
	err = errors.Join(err, model.preparedUpdate.Close())
	if err != nil {
		model.relaunchWarning = "Mihari updated, but installation recovery is required"
	}
	model.preparedUpdate = nil
	return errors.Join(err, finishRun(model, nil, out, relaunch, nil))
}

type discardPreparedResultMsg struct{ err error }

func (m discardPreparedResultMsg) Err() error                       { return m.err }
func (w *runPreparedUpdater) discard(p update.PreparedUpdate) error { return p.Close() }
