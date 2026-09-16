package logging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/platform"
)

type countingFailedWriter struct{ calls int }

func (w *countingFailedWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, errors.New("diagnostic output unavailable")
}

func TestFailureReporter_OutputFailureDoesNotRecurse(t *testing.T) {
	writer := &countingFailedWriter{}
	fixed := time.Unix(100, 0)
	reporter := NewFailureReporter(writer, NewRedactor("fixture-secret"), func() time.Time { return fixed })
	for range 20 {
		reporter.Report(FailureWrite, errors.New("fixture-secret"))
	}
	if writer.calls != 1 {
		t.Fatalf("failed fallback calls=%d want 1", writer.calls)
	}
	fixed = fixed.Add(time.Second)
	reporter.Report(FailureWrite, io.ErrClosedPipe)
	if writer.calls != 2 {
		t.Fatalf("fallback did not resume after window: calls=%d", writer.calls)
	}
}

func TestRotatingWriter_FailuresKeepCountsAndOriginalFallback(t *testing.T) {
	for _, kind := range []string{"lock", "write", "unlock", "apply unlock"} {
		t.Run(kind, func(t *testing.T) {
			w, fs, _ := openTestRotator(t, DefaultConfig())
			var output bytes.Buffer
			w.reporter = NewFailureReporter(&output, NewRedactor("fixture-secret"), func() time.Time { return time.Unix(100, 0) })
			failure := errors.New("fixture-secret /private/fixture logs\r\nfailed")
			if kind != "write" {
				if err := w.lock.Close(); err != nil {
					t.Fatal(err)
				}
				lock := &diagnosticErrorLock{}
				if kind == "lock" {
					lock.lockErr = context.DeadlineExceeded
				} else {
					lock.unlockErr = failure
				}
				w.lock = lock
			} else if err := fs.Close(); err != nil {
				t.Fatal(err)
			}
			record := []byte("{\"msg\":\"record\"}\n")
			cfg := Config{MaxSizeBytes: 2048, MaxFiles: 2}
			for range 20 {
				if kind == "apply unlock" {
					w.Apply(context.Background(), cfg)
					continue
				}
				n, err := w.Write(record)
				if err == nil {
					t.Fatal("failure was not returned")
				}
				wantN := 0
				if kind == "unlock" {
					wantN = len(record)
					if !errors.Is(err, failure) {
						t.Fatal("unlock cause lost")
					}
				}
				if n != wantN {
					t.Fatalf("n=%d want=%d", n, wantN)
				}
			}
			wantDropped := uint64(0)
			if kind == "lock" {
				wantDropped = 20
			}
			if w.Dropped() != wantDropped {
				t.Fatalf("dropped=%d want=%d", w.Dropped(), wantDropped)
			}
			if kind == "apply unlock" && w.config() != cfg {
				t.Fatal("void Apply lost swapped config")
			}
			got := output.String()
			if strings.Count(got, "\n") != 1 {
				t.Fatalf("fallback lines=%d want one", strings.Count(got, "\n"))
			}
			if strings.Contains(got, "\r") {
				t.Fatal("fallback injected carriage return")
			}
			if kind == "unlock" || kind == "apply unlock" {
				if !strings.Contains(got, `fixture-secret /private/fixture logs\r\nfailed`) {
					t.Fatal("fallback discarded original failure")
				}
			}

		})
	}
}

func TestRuntime_CloseJoinsFileAndLockErrorsWithoutClosingSharedFS(t *testing.T) {
	w, fs, paths := openTestRotator(t, DefaultConfig())
	if err := w.lock.Close(); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("lock close failed")
	lock := &diagnosticErrorLock{closeErr: failure}
	w.lock = lock
	f, err := fs.OpenAppend(paths.TUILog)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	w.file = f
	runtime := &Runtime{rotator: w}
	err = runtime.Close()
	if !errors.Is(err, os.ErrClosed) || !errors.Is(err, failure) {
		t.Fatal("close lost file or lock cause")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal("second close changed idempotent return")
	}
	if lock.closes != 1 {
		t.Fatalf("lock closes=%d want 1", lock.closes)
	}
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal("runtime closed shared filesystem")
	}
	if n, err := w.Write([]byte("discarded")); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed writer return changed")
	}
}

type diagnosticErrorLock struct {
	lockErr, unlockErr, closeErr error
	closes                       int
}

func (l *diagnosticErrorLock) Lock(context.Context, platform.LockMode) error { return l.lockErr }
func (l *diagnosticErrorLock) Unlock() error                                 { return l.unlockErr }
func (l *diagnosticErrorLock) Close() error                                  { l.closes++; return l.closeErr }

func TestFailureReporter_PreservesCausePathsAndCredentials(t *testing.T) {
	var output bytes.Buffer
	cause := errors.New("fixture-secret /private/fixture logs\nbody\x1b[31m")
	failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "fixture summary"}, cause)
	NewFailureReporter(&output, NewRedactor("fixture-secret"), nil).Report(FailureWrite, failure)
	got := output.String()
	for _, part := range []string{"fixture summary", "fixture-secret", "/private/fixture logs", `\nbody\x1b[31m`} {
		if !strings.Contains(got, part) {
			t.Fatalf("missing original diagnostic %q", part)
		}
	}
	if strings.Count(got, "\n") != 1 || strings.Contains(got, "\x1b") {
		t.Fatal("fallback executed or injected terminal controls")
	}
}
