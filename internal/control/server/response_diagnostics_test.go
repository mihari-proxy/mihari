package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
)

type failingResponseWriter struct {
	header http.Header
	err    error
}

func (w *failingResponseWriter) Header() http.Header       { return w.header }
func (*failingResponseWriter) WriteHeader(int)             {}
func (w *failingResponseWriter) Write([]byte) (int, error) { return 0, w.err }

func TestHandler_ReportsJSONResponseWriteFailure(t *testing.T) {
	cause := errors.New("write control response fixture")
	var records []diagnostics.Record
	server := New(Options{
		Token:              "token",
		Store:              state.NewStore(state.Snapshot{}),
		DiagnosticReporter: func(_ context.Context, record diagnostics.Record) { records = append(records, record) },
	})
	request, err := http.NewRequest(http.MethodGet, "http://control.invalid/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer token")
	server.Handler().ServeHTTP(&failingResponseWriter{header: make(http.Header), err: cause}, request)
	if len(records) != 1 || records[0].Event != "response.write.failed" || !errors.Is(records[0].Err, cause) {
		t.Fatalf("response write cause missing: %+v", records)
	}
}
