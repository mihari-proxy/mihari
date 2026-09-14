package tui

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/update"
)

type preparedHTTPTransport func(*http.Request) (*http.Response, error)

func (f preparedHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type preparedHTTPBody struct {
	io.Reader
	cause error
}

func (b preparedHTTPBody) Close() error { return b.cause }

func TestPreparedUpdater_HTTPWarningsBorrowLiveReporter(t *testing.T) {
	for _, action := range []string{"check", "prepare"} {
		t.Run(action, func(t *testing.T) {
			cause := errors.New("close fixture update response")
			base := update.SelfUpdater{HTTPClient: &http.Client{Transport: preparedHTTPTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: preparedHTTPBody{strings.NewReader(`{"tag_name":"v1.2.3"}`), cause}}, nil
			})}}
			w := newRunPreparedUpdater(base)
			t.Cleanup(func() {
				if err := w.close(); err != nil {
					t.Error(err)
				}
			})
			var records []diagnostics.Record
			w.diagnostics.Reporter = func(_ context.Context, r diagnostics.Record) { records = append(records, r) }
			var err error
			if action == "check" {
				_, err = w.Check(t.Context(), "v1.2.3", "main")
			} else {
				_, err = w.Prepare(t.Context(), t.TempDir()+"/mihari", "v1.2.3", "main")
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || records[0].Level != slog.LevelWarn || !errors.Is(records[0].Err, cause) {
				t.Fatalf("close warning missing: records=%d", len(records))
			}
			if base.Reporter != nil {
				t.Fatal("borrowed reporter escaped to original updater")
			}
		})
	}
}
