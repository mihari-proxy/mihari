package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/update"
	"io"
	"sync"
)

// runPreparedUpdater joins even a tea.Cmd whose result is discarded on quit.
// Candidate ownership stays here until Run has applied or abandoned the result.
type runPreparedUpdater struct {
	systempage.PreparedSelfUpdater
	next       uint64
	cancels    map[uint64]context.CancelFunc
	mu         sync.Mutex
	closing    bool
	workers    sync.WaitGroup
	candidates []update.PreparedUpdate
}

func newRunPreparedUpdater(u systempage.PreparedSelfUpdater) *runPreparedUpdater {
	return &runPreparedUpdater{PreparedSelfUpdater: u, cancels: map[uint64]context.CancelFunc{}}
}
func (w *runPreparedUpdater) Prepare(ctx context.Context, binary, current, channel string) (update.PreparedUpdate, error) {
	child, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	if w.closing {
		w.mu.Unlock()
		cancel()
		return update.PreparedUpdate{}, context.Canceled
	}
	w.next++
	id := w.next
	w.cancels[id] = cancel
	w.workers.Add(1)
	w.mu.Unlock()
	defer func() { cancel(); w.mu.Lock(); delete(w.cancels, id); w.mu.Unlock(); w.workers.Done() }()
	prepared, err := w.PreparedSelfUpdater.Prepare(child, binary, current, channel)
	w.mu.Lock()
	w.candidates = append(w.candidates, prepared)
	w.mu.Unlock()
	return prepared, err
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
	result, err := apply(ctx, *model.preparedUpdate)
	if !result.Updated {
		return err
	}
	err = errors.Join(err, model.preparedUpdate.Close())
	if err != nil {
		model.relaunchWarning = "Mihari updated, but installation recovery is required"
	}
	model.preparedUpdate = nil
	return errors.Join(err, finishRun(model, nil, out, relaunch, nil))
}
