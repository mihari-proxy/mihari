package tui

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/logging"
)

type shutdownErrorCloser struct {
	err   error
	close func()
}

func (c shutdownErrorCloser) Close() error {
	if c.close != nil {
		c.close()
	}
	return c.err
}

func TestRunCleanup_CloseFailureReportsAfterAllResourcesOnce(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil logger", true: "owned closer"}[available], func(t *testing.T) {
			var order []string
			logErr := errors.New("close /private/tui-secret/log\r\nfailed")
			fsErr := errors.New("close C:\\Users\\tui-secret\\logs")
			var warnings bytes.Buffer
			reporter := newTUILoggingFailureReporter(&warnings, logging.NewRedactor("tui-secret"), func() time.Time { return time.Unix(100, 0) })
			var logger io.Closer
			if available {
				logger = shutdownErrorCloser{err: logErr, close: func() { order = append(order, "logging") }}
			}
			resources := LoggingResources{closeState: newLoggingResourcesCloseState(logger, shutdownErrorCloser{err: fsErr, close: func() {
				order = append(order, "fs")
				if warnings.Len() != 0 {
					t.Fatal("cleanup warning before all resources closed")
				}
			}})}
			cleanup := newRunCleanup(&resources, func() { order = append(order, "cancel") }, func() { order = append(order, "session") }, nil, &orderedLoggingApplier{order: &order}, reporter)
			first := cleanup(nil)
			second := cleanup(nil)
			if first != second || !errors.Is(first, fsErr) || (available && !errors.Is(first, logErr)) {
				t.Fatal("cleanup changed cached joined errors")
			}
			want := []string{"cancel", "session", "applier", "fs"}
			if available {
				want = []string{"cancel", "session", "applier", "logging", "fs"}
			}
			if !slices.Equal(order, want) {
				t.Fatalf("close order=%q want=%q", order, want)
			}
			if warnings.String() != "Warning: TUI file logging cleanup failed\n" {
				t.Fatal("cleanup warning leaked details or duplicated")
			}
		})
	}
}

type failedTUIWarningWriter struct{ calls int }

func (w *failedTUIWarningWriter) Write([]byte) (int, error) { w.calls++; return 0, io.ErrClosedPipe }
func TestTUILoggingFailureReporter_OutputFailureDoesNotRecurse(t *testing.T) {
	writer := &failedTUIWarningWriter{}
	now := time.Unix(100, 0)
	reporter := newTUILoggingFailureReporter(writer, nil, func() time.Time { return now })
	for range 20 {
		reporter.report(tuiLoggingBootstrapFailure, io.ErrClosedPipe)
		reporter.report(tuiLoggingCleanupFailure, io.ErrClosedPipe)
	}
	if writer.calls != 2 {
		t.Fatalf("fallback attempts=%d want one per kind", writer.calls)
	}
	now = now.Add(time.Second)
	reporter.report(tuiLoggingCleanupFailure, io.ErrClosedPipe)
	if writer.calls != 3 {
		t.Fatal("existing rate limit did not lift")
	}
}
