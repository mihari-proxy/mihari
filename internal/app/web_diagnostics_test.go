package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

func TestWebMutatorDiagnostics_ContextOutcomeAndID(t *testing.T) {
	for _, action := range []struct {
		name, prefix string
		call         func(webMutator, context.Context) error
		operation    func(*recordingWebMutationRuntime) runtimeapi.Operation
	}{
		{"proxy.select", "web-select-", func(m webMutator, c context.Context) error { return m.SelectProxy(c, "private-group", "private-node") }, func(r *recordingWebMutationRuntime) runtimeapi.Operation { return r.selectCalls[0].operation }},
		{"connection.close", "web-close-", func(m webMutator, c context.Context) error { return m.CloseConnection(c, "private-connection") }, func(r *recordingWebMutationRuntime) runtimeapi.Operation { return r.closeCalls[0].operation }},
		{"connection.close_all", "web-close-all-", func(m webMutator, c context.Context) error { return m.CloseAllConnections(c) }, func(r *recordingWebMutationRuntime) runtimeapi.Operation { return r.closeAllOperations[0] }},
		{"tun.enable", "web-tun-", func(m webMutator, c context.Context) error {
			return m.ApplyConfigPatch(c, map[string]any{"tun": map[string]any{"enable": true}})
		}, func(r *recordingWebMutationRuntime) runtimeapi.Operation { return r.enableOperations[0] }},
		{"tun.disable", "web-tun-", func(m webMutator, c context.Context) error {
			return m.ApplyConfigPatch(c, map[string]any{"tun": map[string]any{"enable": false}})
		}, func(r *recordingWebMutationRuntime) runtimeapi.Operation { return r.disableOperations[0] }},
	} {
		for _, outcome := range []struct {
			name   string
			err    error
			level  string
			cancel bool
		}{
			{"success", nil, "INFO", false}, {"expected", protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "private-api-message"}, "DEBUG", false},
			{"failure", &os.PathError{Op: "write", Path: "/private/controller-secret", Err: os.ErrPermission}, "ERROR", false},
			{"cancel", context.Canceled, "", true}, {"live deadline", context.DeadlineExceeded, "ERROR", false},
		} {
			t.Run(action.name+"/"+outcome.name, func(t *testing.T) {
				var out bytes.Buffer
				level := new(slog.LevelVar)
				level.Set(slog.LevelDebug)
				redactor := logging.NewRedactor()
				reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&out, level, "daemon", redactor)), redactor)
				r := &recordingWebMutationRuntime{err: outcome.err}
				now := time.Date(2026, 9, 10, 1, 2, 3, 456, time.UTC)
				m := webMutator{manager: r, reporter: reporter, now: func() time.Time { return now }, readRandom: func(b []byte) (int, error) { clear(b); return len(b), nil }}
				ctx, cancel := context.WithCancel(logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "stale-parent", Name: "stale.parent"}))
				defer cancel()
				if outcome.cancel {
					cancel()
				}
				err := action.call(m, ctx)
				if expected, ok := outcome.err.(protocol.APIError); ok {
					var actual protocol.APIError
					if !errors.As(err, &actual) || actual.Code != expected.Code || actual.Message != expected.Message {
						t.Fatal("mutation changed API error")
					}
				} else if !errors.Is(err, outcome.err) {
					t.Fatal("mutation changed error identity")
				}
				op := action.operation(r)
				wantID := action.prefix + now.Format("20060102T150405.000000000")
				if action.prefix == "web-tun-" {
					wantID = action.prefix + strings.Repeat("0", 32)
				}
				if op.ID != wantID || op.Source != "web" {
					t.Errorf("operation ID strategy mismatch: %q", op.ID)
				}
				metadata, ok := logging.OperationFromContext(r.contexts[0])
				if !ok || metadata.ID != op.ID || metadata.Name != action.name {
					t.Errorf("manager entry metadata=%+v, present=%v", metadata, ok)
				}
				records := decodeSchedulerDiagnostics(t, out.String())
				if outcome.level == "" {
					if len(records) != 0 {
						t.Fatal("normal cancellation logged")
					}
					return
				}
				if len(records) != 1 {
					t.Fatalf("records=%d, want 1", len(records))
				}
				record := records[0]
				event := "mutation.failed"
				if err == nil {
					event = "mutation.succeeded"
				}
				if record["component"] != "web" || record["msg"] != event || record["level"] != outcome.level || record["operation_id"] != op.ID || record["operation"] != action.name {
					t.Fatalf("unexpected diagnostic: %#v", record)
				}
				if err != nil && !diagnostics.AlreadyReported(err) {
					t.Fatal("reported mutation lacks marker")
				}
				for _, secret := range []string{"private-group", "private-node", "private-connection", "private-api-message", "/private/controller-secret"} {
					if strings.Contains(out.String(), secret) {
						t.Fatal("diagnostic leaked private data")
					}
				}
			})
		}
	}
}

func TestWebMutatorDiagnostics_TUNRandomFallback(t *testing.T) {
	r := &recordingWebMutationRuntime{}
	now := time.Date(2026, 9, 10, 2, 3, 4, 567, time.UTC)
	m := webMutator{manager: r, now: func() time.Time { return now }, readRandom: func([]byte) (int, error) { return 0, os.ErrPermission }}
	if err := m.ApplyConfigPatch(context.Background(), map[string]any{"tun": map[string]any{"enable": true}}); err != nil {
		t.Fatal(err)
	}
	if got := r.enableOperations[0].ID; got != "web-tun-"+now.Format("20060102T150405.000000000") {
		t.Fatalf("fallback ID=%q", got)
	}
}

func TestWebMutatorDiagnostics_RealManagerOwnsFailure(t *testing.T) {
	var out bytes.Buffer
	redactor := logging.NewRedactor()
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&out, new(slog.LevelVar), "daemon", redactor)), redactor)
	manager := runtimeapi.New(runtimeapi.Options{DiagnosticReporter: reporter})
	m := webMutator{manager: manager, reporter: reporter}
	err := m.SelectProxy(context.Background(), "private-group", "private-node")
	if !diagnostics.AlreadyReported(err) {
		t.Fatal("real Manager failure missing marker")
	}
	records := decodeSchedulerDiagnostics(t, out.String())
	if len(records) != 1 || records[0]["component"] != "runtime" || records[0]["level"] != "ERROR" {
		t.Fatalf("expected only runtime failure: %#v", records)
	}
}

func TestBuildRuntimeWithOptionsWiresWebDiagnostics(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	redactor := logging.NewRedactor()
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "daemon", redactor)), redactor)
	settings := testRuntimeSettings(t)
	settings.ControllerSecret = strings.Repeat("d", 64)
	assembly, err := BuildRuntimeWithOptions(paths, settings, "test-version", nil, nil, RuntimeBuildOptions{DiagnosticReporter: reporter})
	if err != nil {
		t.Fatal(err)
	}
	if assembly.Web.Reporter == nil {
		t.Fatal("server reporter not wired")
	}
	mutator, ok := assembly.Web.Mutator.(webMutator)
	if !ok || mutator.reporter == nil {
		t.Fatal("adapter reporter not wired")
	}
	output.Reset()
	assembly.Web.Proxy.Transport = schedulerTransport(func(*http.Request) (*http.Response, error) { return nil, os.ErrPermission })
	recorder := httptest.NewRecorder()
	assembly.Web.Proxy.ServeHTTP(recorder, httptest.NewRequest("GET", "/version", nil))
	records := decodeSchedulerDiagnostics(t, output.String())
	if recorder.Code != http.StatusBadGateway || len(records) != 1 || records[0]["component"] != "web" || records[0]["msg"] != "proxy.failed" || records[0]["level"] != "ERROR" {
		t.Fatal("assembled proxy failure diagnostics missing")
	}
}
