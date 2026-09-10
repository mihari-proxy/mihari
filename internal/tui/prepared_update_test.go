package tui

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/update"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestPreparedUpdate_CleanupBeforeApplyAndRelaunch(t *testing.T) {
	for _, failure := range []string{"", "program", "cleanup", "apply", "after-rename"} {
		t.Run(failure, func(t *testing.T) {
			model := NewModel()
			model.relaunchRequested = true
			model.preparedUpdate = &update.PreparedUpdate{Available: true, CandidatePath: "candidate"}
			events := []string{}
			var warnings bytes.Buffer
			var runErr error
			if failure == "program" {
				runErr = errors.New("program")
			}
			err := finishPreparedRun(context.Background(), model, runErr, &warnings, func() error { events = append(events, "relaunch"); return nil }, func(tea.Model) error {
				events = append(events, "workers-logging-fs-closed")
				if failure == "cleanup" {
					return errors.New("cleanup")
				}
				return nil
			}, func(context.Context, update.PreparedUpdate) (update.Result, error) {
				events = append(events, "apply")
				if failure == "apply" {
					return update.Result{}, errors.New("apply")
				}
				if failure == "after-rename" {
					return update.Result{Updated: true}, errors.New("sync /private/late-secret/data\r\nfailed")
				}
				return update.Result{Updated: true}, nil
			})
			want := []string{"workers-logging-fs-closed"}
			if failure != "program" && failure != "cleanup" {
				want = append(want, "apply")
				if failure != "apply" {
					want = append(want, "relaunch")
				}
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("unsafe update order: %v want %v err=%v", events, want, err)
			}
			if failure == "after-rename" {
				if warnings.String() != "Warning: Mihari updated, but installation recovery is required\n" {
					t.Fatal("post-cleanup warning missing or leaked raw cause")
				}
			} else if warnings.Len() != 0 {
				t.Fatal("unexpected post-cleanup output")
			}
			if failure != "" && err == nil {
				t.Fatal("failure discarded")
			}
		})
	}
}

type blockingPreparedUpdater struct{ started, canceled, allow chan struct{} }

func (*blockingPreparedUpdater) Check(context.Context, string, string) (update.CheckResult, error) {
	return update.CheckResult{}, nil
}
func (*blockingPreparedUpdater) Update(context.Context, string, string, string) (update.Result, error) {
	panic("Update called during TUI")
}
func (u *blockingPreparedUpdater) Prepare(ctx context.Context, _, _, _ string) (update.PreparedUpdate, error) {
	close(u.started)
	<-ctx.Done()
	close(u.canceled)
	<-u.allow
	return update.PreparedUpdate{}, ctx.Err()
}
func (*blockingPreparedUpdater) ApplyPrepared(context.Context, update.PreparedUpdate) (update.Result, error) {
	return update.Result{}, nil
}
func TestPreparedUpdate_RunCancelsAndJoinsDownload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updater := &blockingPreparedUpdater{started: make(chan struct{}), canceled: make(chan struct{}), allow: make(chan struct{})}
	worker := newRunPreparedUpdater(updater)
	done := make(chan struct{})
	go func() { defer close(done); _, _ = worker.Prepare(ctx, "binary", "v1", "main") }()
	<-updater.started
	shutdown := make(chan struct{})
	go func() { worker.shutdown(); close(shutdown) }()
	select {
	case <-shutdown:
		cancel()
		close(updater.allow)
		<-done
		t.Fatal("Run abandoned active Prepare before cancel/join")
	case <-updater.canceled:
	}
	select {
	case <-shutdown:
		close(updater.allow)
		<-done
		t.Fatal("Run abandoned canceled Prepare before join")
	default:
	}
	close(updater.allow)
	<-done
	<-shutdown
	if err := worker.close(); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedUpdate_ApplyFailureRequiresReopening(t *testing.T) {
	model := NewModel()
	model.preparedUpdate = &update.PreparedUpdate{Available: true}
	err := finishPreparedRun(context.Background(), model, nil, io.Discard, func() error { t.Fatal("relaunch after failed apply"); return nil }, func(tea.Model) error { return nil }, func(context.Context, update.PreparedUpdate) (update.Result, error) {
		return update.Result{}, errors.New("installation changed")
	})
	if err == nil || !strings.Contains(err.Error(), "reopen Mihari and retry") {
		t.Fatalf("missing retry instruction: %v", err)
	}
}
