package tui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
)

func TestPreparedUpdate_ReportsApplyStatusBeforeRelaunch(t *testing.T) {
	for _, outcome := range []string{"success", "failed", "recovery", "not applied"} {
		t.Run(outcome, func(t *testing.T) {
			m := NewModel()
			m.relaunchRequested = true
			m.preparedUpdate = &update.PreparedUpdate{Available: true, Version: "v1.2.3"}
			var out bytes.Buffer
			want := "Mihari updated to v1.2.3"
			switch outcome {
			case "failed":
				want = "Mihari update failed"
			case "recovery":
				want = "Mihari updated; recovery required"
			case "not applied":
				want = "Mihari update not applied"
			}
			relaunched := false
			err := finishPreparedRun(context.Background(), m, nil, &out, func() error {
				relaunched = true
				if !strings.Contains(out.String(), want) {
					t.Fatalf("result missing before relaunch: %q", out.String())
				}
				return nil
			}, nil, func(context.Context, update.PreparedUpdate) (update.Result, error) {
				if !strings.Contains(out.String(), "Updating Mihari to v1.2.3") {
					t.Fatalf("apply began without progress: %q", out.String())
				}
				switch outcome {
				case "failed":
					return update.Result{}, errors.New("fixture apply failed")
				case "recovery":
					return update.Result{Updated: true}, errors.New("fixture recovery required")
				case "not applied":
					return update.Result{}, nil
				default:
					return update.Result{Updated: true}, nil
				}
			})
			if !strings.Contains(out.String(), want) {
				t.Fatalf("missing result: %q", out.String())
			}
			if relaunched != (outcome == "success" || outcome == "recovery") {
				t.Fatalf("unexpected relaunch: %v", relaunched)
			}
			if (err != nil) != (outcome == "failed" || outcome == "recovery") {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(out.String(), "\r") {
				t.Fatal("non-terminal output contains animation control characters")
			}
		})
	}
}

type updateProgressWriter struct {
	writes chan string
	err    error
}

func (w updateProgressWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	w.writes <- string(p)
	return len(p), nil
}

func TestMihariUpdateProgress_AnimatesAndJoinsBeforeFinalStatus(t *testing.T) {
	writes := make(chan string, 4)
	ticks := make(chan time.Time)
	finish := mihariUpdateProgress(updateProgressWriter{writes: writes}, "Updating Mihari", ticks)
	if got := <-writes; !strings.Contains(got, "⠋ Updating Mihari") {
		t.Fatalf("missing initial frame: %q", got)
	}
	for frame := 1; frame <= 3; frame++ {
		at := time.Unix(0, int64(time.Duration(frame)*spinnerTickInterval))
		ticks <- at
		if got := <-writes; got != "\r"+ui.SpinnerLabel(at, "Updating Mihari") {
			t.Fatalf("wrong animation frame: %q", got)
		}
	}
	if err := finish("Mihari updated"); err != nil {
		t.Fatal(err)
	}
	if got := <-writes; got != "\r\x1b[2KMihari updated\n" {
		t.Fatalf("wrong final status: %q", got)
	}
	select {
	case ticks <- time.Unix(1, 0):
		t.Fatal("animation worker survived completion")
	default:
	}
}

func TestMihariUpdateProgress_WriteFailurePreservedAndWorkerJoined(t *testing.T) {
	failure := errors.New("fixture progress output failed")
	for _, animate := range []bool{false, true} {
		var ticks chan time.Time
		if animate {
			ticks = make(chan time.Time)
		}
		finish := mihariUpdateProgress(updateProgressWriter{err: failure}, "Updating Mihari", ticks)
		if err := finish("Mihari updated"); !errors.Is(err, failure) {
			t.Fatalf("lost write failure: %v", err)
		}
		if animate {
			select {
			case ticks <- time.Unix(1, 0):
				t.Fatal("failed output left animation worker running")
			default:
			}
		}
	}
}

func TestPreparedUpdate_ProgressFailureDoesNotPreventApplyOrRelaunch(t *testing.T) {
	m := NewModel()
	m.relaunchRequested = true
	m.preparedUpdate = &update.PreparedUpdate{Available: true, Version: "v1.2.3"}
	failure := errors.New("fixture progress output failed")
	called := false
	err := finishPreparedRun(context.Background(), m, nil, updateProgressWriter{err: failure}, func() error {
		called = true
		return nil
	}, nil, func(context.Context, update.PreparedUpdate) (update.Result, error) {
		return update.Result{Updated: true}, nil
	})
	if !called || !errors.Is(err, failure) {
		t.Fatalf("progress failure changed update result or was lost: relaunch=%v err=%v", called, err)
	}
}
