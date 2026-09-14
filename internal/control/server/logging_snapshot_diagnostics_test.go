package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type failingSnapshotResponseWriter struct {
	header      http.Header
	deadlineErr error
	flushErr    error
}

func (w *failingSnapshotResponseWriter) Header() http.Header { return w.header }
func (*failingSnapshotResponseWriter) WriteHeader(int)       {}
func (*failingSnapshotResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
func (w *failingSnapshotResponseWriter) SetWriteDeadline(time.Time) error { return w.deadlineErr }
func (w *failingSnapshotResponseWriter) FlushError() error                { return w.flushErr }

func TestWriteSnapshotFrame_PreservesDeadlineAndFlushCauses(t *testing.T) {
	for _, phase := range []string{"deadline", "flush"} {
		t.Run(phase, func(t *testing.T) {
			cause := errors.New(phase + " snapshot response fixture")
			writer := &failingSnapshotResponseWriter{header: make(http.Header)}
			if phase == "deadline" {
				writer.deadlineErr = cause
			} else {
				writer.flushErr = cause
			}
			if err := writeSnapshotFrame(writer, []byte("{}\n")); !errors.Is(err, cause) {
				t.Fatalf("%s cause lost: %v", phase, err)
			}
		})
	}
}

func TestLoggingSnapshot_ReportsOriginalCauseWithSafeWireError(t *testing.T) {
	for _, phase := range []string{"open", "read"} {
		t.Run(phase, func(t *testing.T) {
			cause := errors.New("read /private/snapshot?token=fixture: permission denied")
			source := newFixtureSnapshotSource()
			if phase == "open" {
				source.openErr = cause
			} else {
				source.daemon.nextErr = cause
			}
			server := newSnapshotServer(t, source)
			var records []diagnostics.Record
			server.diagnosticReporter = func(_ context.Context, record diagnostics.Record) { records = append(records, record) }
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, snapshotRequest(`{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`))
			if len(records) != 1 || !errors.Is(records[0].Err, cause) || records[0].Level != slog.LevelError {
				t.Fatalf("snapshot cause/owner missing: %+v", records)
			}
			if strings.Contains(recorder.Body.String(), "fixture") || strings.Contains(recorder.Body.String(), "/private/") {
				t.Fatal("internal cause entered snapshot wire error")
			}
		})
	}
}

func TestLoggingSnapshot_RecordsExpectedRejection(t *testing.T) {
	server := newSnapshotServer(t, newFixtureSnapshotSource())
	var records []diagnostics.Record
	server.diagnosticReporter = func(_ context.Context, record diagnostics.Record) { records = append(records, record) }
	server.Handler().ServeHTTP(httptest.NewRecorder(), snapshotRequest("{}"))
	if len(records) != 1 || records[0].Level != slog.LevelInfo || records[0].Err == nil {
		t.Fatalf("snapshot rejection missing: %+v", records)
	}
}
