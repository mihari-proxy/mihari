package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/update"
)

type preparedHTTPTransport func(*http.Request) (*http.Response, error)

func (f preparedHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type preparedHTTPBody struct {
	io.Reader
	cause error
}

func (b preparedHTTPBody) Close() error { return b.cause }

type checkDiagnosticUpdater struct {
	blockingPreparedUpdater
	err error
}

func (u checkDiagnosticUpdater) Check(context.Context, string, string) (update.CheckResult, error) {
	return update.CheckResult{}, u.err
}

func TestPreparedUpdater_CheckPreservesUnreportedAndReportedResults(t *testing.T) {
	for _, reported := range []bool{false, true} {
		cause := error(io.ErrUnexpectedEOF)
		if reported {
			cause = diagnostics.MarkReported(cause)
		}
		worker := newRunPreparedUpdater(&checkDiagnosticUpdater{err: cause})
		if reported {
			worker.diagnostics.Reporter = func(context.Context, diagnostics.Record) {
				t.Error("already reported check failure was logged twice")
			}
		}
		_, err := worker.Check(t.Context(), "v0.9.3", update.ChannelMain)
		if err != cause {
			t.Fatal("check changed the error without a new diagnostic")
		}
		if err := worker.close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPreparedUpdater_CheckFailureReportsCauseOnce(t *testing.T) {
	for _, channel := range []string{update.ChannelMain, update.ChannelDev} {
		for _, failure := range []string{"transport", "timeout", "http", "decode", "canceled", "success"} {
			t.Run(channel+"/"+failure, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				cause := errors.New("fixture transport failure")
				var requestOperation logging.OperationMetadata
				base := update.SelfUpdater{HTTPClient: &http.Client{Transport: preparedHTTPTransport(func(r *http.Request) (*http.Response, error) {
					requestOperation, _ = logging.OperationFromContext(r.Context())
					switch failure {
					case "transport":
						return nil, cause
					case "timeout":
						return nil, context.DeadlineExceeded
					case "canceled":
						cancel()
						return nil, r.Context().Err()
					case "http":
						return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader(`{"message":"fixture rate limit"}`))}, nil
					case "decode":
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{`))}, nil
					default:
						body := `{"tag_name":"v1.2.3"}`
						if channel == update.ChannelDev {
							body = `[{"tag_name":"v1.2.3-dev.1","assets":[{"name":"fixture"}]}]`
						}
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
					}
				})}}
				w := newRunPreparedUpdater(base)
				t.Cleanup(func() {
					if err := w.close(); err != nil {
						t.Error(err)
					}
				})
				var records []diagnostics.Record
				var output bytes.Buffer
				reporter := localTaskJSON(&output)
				w.diagnostics.Reporter = func(ctx context.Context, r diagnostics.Record) {
					operation, _ := logging.OperationFromContext(ctx)
					if operation != requestOperation || operation.ID == "" || operation.Name != "self.check" {
						t.Error("check diagnostic lost its execution identity")
					}
					if !w.mu.TryLock() {
						t.Error("reporter called while holding worker mutex")
					} else {
						if len(w.cancels) != 1 {
							t.Error("check worker finished before reporting its failure")
						}
						w.mu.Unlock()
					}
					records = append(records, r)
					reporter(ctx, r)
				}
				result, err := w.Check(ctx, "v1.0.0", channel)
				if failure == "success" {
					if err != nil || !result.Available || len(records) != 0 {
						t.Fatal("successful check changed or reported a failure")
					}
					return
				}
				if err == nil {
					t.Fatal("check failure lost")
				}
				if failure == "canceled" {
					if len(records) != 0 {
						t.Fatal("canceled check was logged as a failure")
					}
					return
				}
				if len(records) != 1 || records[0].Event != "self.check.failed" || records[0].Level != slog.LevelError || !diagnostics.AlreadyReported(err) {
					t.Fatalf("missing check failure diagnostic: records=%d", len(records))
				}
				var logged struct {
					Message   string `json:"msg"`
					Cause     string `json:"cause"`
					Operation string `json:"operation"`
					ID        string `json:"operation_id"`
				}
				if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &logged); err != nil {
					t.Fatalf("expected one JSON log record: %v", err)
				}
				if logged.Message != "self.check.failed" || logged.Operation != "self.check" || logged.ID != requestOperation.ID || logged.Cause == "" {
					t.Fatal("check diagnostic missing from logger output")
				}
				var api protocol.APIError
				if !errors.As(err, &api) || strings.Contains(err.Error(), "fixture") {
					t.Fatal("public error changed or exposed internal details")
				}
				if failure == "timeout" && (!errors.Is(err, context.DeadlineExceeded) || !strings.Contains(logged.Cause, "context deadline exceeded")) {
					t.Fatal("timeout cause missing from logger output")
				}
				if failure == "transport" && (!errors.Is(err, cause) || !errors.Is(records[0].Err, cause)) {
					t.Fatal("transport cause lost")
				}
				if failure == "http" {
					var detail *diagnostics.HTTPError
					if !errors.As(records[0].Err, &detail) || detail.Status != http.StatusForbidden || !strings.Contains(detail.Body, "fixture rate limit") {
						t.Fatal("upstream diagnostic lost")
					}
					if !strings.Contains(logged.Cause, "HTTP 403") || !strings.Contains(logged.Cause, "fixture rate limit") {
						t.Fatal("upstream status or body missing from logger output")
					}
				}
			})
		}
	}
}

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
