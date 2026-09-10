package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"go.yaml.in/yaml/v3"
)

func TestNewDiagnosticReporter_DiagnosticOutputIsSafe(t *testing.T) {
	const secret = "registered-secret"
	var output bytes.Buffer
	redactor := NewRedactor(secret)
	logger := slog.New(NewJSONHandler(&output, diagnosticTestLevel(slog.LevelDebug), "daemon", redactor))
	reporter := NewDiagnosticReporter(logger, redactor)
	ctx := WithOperation(context.Background(), OperationMetadata{ID: "op-settings-1", Name: "settings.update"})

	path := "/home/private user/" + secret + "/mihari.yaml"
	rawURL := "https://user:password@example.test/config?token=" + secret
	cause := errors.Join(
		fmt.Errorf("save settings: %w", &os.PathError{Op: "rename", Path: path, Err: os.ErrPermission}),
		&url.Error{Op: "Get", URL: rawURL, Err: syscall.ECONNREFUSED},
	)
	err := diagnostics.Wrap(protocol.APIError{
		Code:    protocol.CodeDataFailure,
		Message: "settings update failed",
		Details: map[string]any{"path": path},
	}, cause)
	if err.Error() != "settings update failed" {
		t.Fatalf("public Error() = %q", err.Error())
	}

	reporter(ctx, diagnostics.Record{Component: "daemon.settings", Event: "operation_failed", Level: slog.LevelError, Err: err})
	record := decodeDiagnosticRecord(t, output.Bytes())
	causeText := jsonStringField(t, record, "cause")
	for _, want := range []string{"rename", "permission denied", "Get", "connection refused", "data_failure"} {
		if !strings.Contains(causeText, want) {
			t.Fatalf("cause %q does not contain safe diagnostic %q", causeText, want)
		}
	}
	for _, forbidden := range []string{path, secret, rawURL, "user:password", "mihari.yaml"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("diagnostic output leaked %q: %s", forbidden, output.String())
		}
	}
	if got := jsonStringField(t, record, "operation_id"); got != "op-settings-1" {
		t.Fatalf("operation_id = %q", got)
	}
	if got := jsonStringField(t, record, "component"); got != "daemon.settings" {
		t.Fatalf("component = %q", got)
	}
	if got := jsonStringField(t, record, "msg"); got != "operation_failed" {
		t.Fatalf("msg = %q", got)
	}
}

func TestNewDiagnosticReporter_DiagnosticTextIsBoundedAfterRedaction(t *testing.T) {
	const secret = "boundary-secret-value"
	var output bytes.Buffer
	redactor := NewRedactor(secret)
	logger := slog.New(NewJSONHandler(&output, diagnosticTestLevel(slog.LevelDebug), "daemon", redactor))
	reporter := NewDiagnosticReporter(logger, redactor)
	rawOperation := strings.Repeat("界", 1360) + secret + " https://example.test/private?token=" + secret

	reporter(context.Background(), diagnostics.Record{
		Component: "daemon.settings",
		Event:     "operation_failed",
		Level:     slog.LevelError,
		Err: &os.PathError{
			Op: rawOperation, Path: "relative/private.yaml", Err: io.ErrUnexpectedEOF,
		},
	})
	record := decodeDiagnosticRecord(t, output.Bytes())
	causeText := jsonStringField(t, record, "cause")
	if len(causeText) > 4096 {
		t.Fatalf("cause has %d bytes, want at most 4096", len(causeText))
	}
	if !utf8.ValidString(causeText) {
		t.Fatal("bounded diagnostic is not valid UTF-8")
	}
	if !strings.Contains(causeText, "path operation") {
		t.Fatalf("cause lost typed operation summary: %q", causeText)
	}
	if strings.Contains(causeText, secret) || strings.Contains(causeText, "boundary-sec") || strings.Contains(causeText, "https://") {
		t.Fatalf("redaction happened after truncation: %q", causeText)
	}
}

func TestNewDiagnosticReporter_UnknownTextUsesConservativeSummary(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "credentialed alternate URL", raw: "dial socks5://alice:hunter2@example.test"},
		{name: "relative filename", raw: "open configs/private-settings.yaml"},
		{name: "multiline config and command output", raw: "config:\r\n  password: hunter2\r\ncommand output:\nprivate payload"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			redactor := NewRedactor()
			logger := slog.New(NewJSONHandler(&output, diagnosticTestLevel(slog.LevelDebug), "daemon", redactor))
			NewDiagnosticReporter(logger, redactor)(context.Background(), diagnostics.Record{
				Component: "daemon.settings", Event: "operation_failed", Level: slog.LevelError, Err: errors.New(test.raw),
			})
			cause := jsonStringField(t, decodeDiagnosticRecord(t, output.Bytes()), "cause")
			if cause != "error (*errors.errorString)" {
				t.Fatalf("cause = %q, want conservative type summary", cause)
			}
			for _, forbidden := range []string{"socks5://", "alice", "hunter2", "configs/", "private-settings.yaml", "private payload", "command output"} {
				if strings.Contains(output.String(), forbidden) {
					t.Fatalf("unknown diagnostic leaked %q: %s", forbidden, output.String())
				}
			}
		})
	}
}

func TestNewDiagnosticReporter_UsesSafeSummariesForSensitiveErrorTypes(t *testing.T) {
	const raw = "token: config-secret https://example.test/config /private/settings.yaml"
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "yaml", err: &yaml.TypeError{Errors: []string{raw}}, want: "configuration parse error"},
		{name: "command output", err: &exec.ExitError{Stderr: []byte(raw)}, want: "command execution failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(NewJSONHandler(&output, diagnosticTestLevel(slog.LevelDebug), "daemon", NewRedactor()))
			NewDiagnosticReporter(logger, NewRedactor())(context.Background(), diagnostics.Record{
				Component: "daemon.settings", Event: "operation_failed", Level: slog.LevelError, Err: test.err,
			})
			causeText := jsonStringField(t, decodeDiagnosticRecord(t, output.Bytes()), "cause")
			if !strings.Contains(causeText, test.want) {
				t.Fatalf("cause = %q, want safe summary %q", causeText, test.want)
			}
			for _, forbidden := range []string{"config-secret", "https://", "/private", "settings.yaml"} {
				if strings.Contains(output.String(), forbidden) {
					t.Fatalf("diagnostic output leaked %q: %s", forbidden, output.String())
				}
			}
		})
	}
}

func TestNewDiagnosticReporter_NilRedactorAndCycleUseSafeSummary(t *testing.T) {
	const raw = "registered-secret https://example.test/private /private/settings.yaml"
	cycle := &diagnosticCycle{message: raw}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reporter := NewDiagnosticReporter(logger, nil)
	reporter(context.Background(), diagnostics.Record{
		Component: "daemon.settings", Event: "operation_failed", Level: slog.LevelError,
		Err: fmt.Errorf("outer unsafe text: %w", cycle),
	})
	causeText := jsonStringField(t, decodeDiagnosticRecord(t, output.Bytes()), "cause")
	if causeText == "" {
		t.Fatal("nil-redactor diagnostic summary is empty")
	}
	for _, forbidden := range []string{"registered-secret", "https://", "/private", "settings.yaml", "outer unsafe text"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("nil-redactor output leaked %q: %s", forbidden, output.String())
		}
	}
}

func TestNewDiagnosticReporter_UnusualErrorValuesUseSafeSummary(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "non-comparable value", err: diagnosticSliceError{parts: []string{"private", "content"}}},
		{name: "typed nil pointer", err: (*os.PathError)(nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
			NewDiagnosticReporter(logger, nil)(context.Background(), diagnostics.Record{
				Component: "daemon.settings", Event: "operation_failed", Level: slog.LevelError, Err: test.err,
			})
			if cause := jsonStringField(t, decodeDiagnosticRecord(t, output.Bytes()), "cause"); cause == "" {
				t.Fatal("safe summary is empty")
			}
		})
	}
}

func TestNewDiagnosticReporter_TraversalBoundsCountRootAndFanoutWork(t *testing.T) {
	withinDepth := error(os.ErrPermission)
	for range 31 {
		withinDepth = &diagnosticDepthWrap{cause: withinDepth}
	}
	if cause := reportedDiagnosticCause(t, withinDepth); !strings.Contains(cause, "permission denied") || strings.Contains(cause, "truncated") {
		t.Fatalf("32-layer cause = %q", cause)
	}

	beyondDepth := error(os.ErrPermission)
	for range 32 {
		beyondDepth = &diagnosticDepthWrap{cause: beyondDepth}
	}
	if cause := reportedDiagnosticCause(t, beyondDepth); !strings.Contains(cause, "diagnostic graph truncated") || strings.Contains(cause, "permission denied") {
		t.Fatalf("33-layer cause = %q", cause)
	}

	shared := error(os.ErrPermission)
	withinNodes := make([]error, 63)
	for i := range withinNodes {
		withinNodes[i] = shared
	}
	if cause := reportedDiagnosticCause(t, diagnosticMulti{children: withinNodes}); strings.Contains(cause, "truncated") {
		t.Fatalf("64-node repeated DAG was truncated: %q", cause)
	}

	wide := make([]error, 64)
	wide[63] = diagnosticPanicUnwrap{}
	if cause := reportedDiagnosticCause(t, diagnosticMulti{children: wide}); !strings.Contains(cause, "diagnostic graph truncated") {
		t.Fatalf("fanout beyond budget = %q", cause)
	}
}

func TestNewDiagnosticReporter_HonorsLoggerLevelAndNilLogger(t *testing.T) {
	if reporter := NewDiagnosticReporter(nil, NewRedactor()); reporter != nil {
		t.Fatal("nil logger returned a reporter")
	}
	var output bytes.Buffer
	logger := slog.New(NewJSONHandler(&output, diagnosticTestLevel(slog.LevelWarn), "daemon", NewRedactor()))
	reporter := NewDiagnosticReporter(logger, NewRedactor())
	reporter(context.Background(), diagnostics.Record{Event: "expected_conflict", Level: slog.LevelDebug, Err: errors.New("conflict")})
	if output.Len() != 0 {
		t.Fatalf("disabled diagnostic was logged: %s", output.String())
	}
}

type diagnosticCycle struct {
	message string
}

func (e *diagnosticCycle) Error() string { return e.message }
func (e *diagnosticCycle) Unwrap() error { return e }

type diagnosticSliceError struct {
	parts []string
}

func (e diagnosticSliceError) Error() string { return strings.Join(e.parts, " ") }

type diagnosticDepthWrap struct{ cause error }

func (*diagnosticDepthWrap) Error() string   { return "wrapped" }
func (e *diagnosticDepthWrap) Unwrap() error { return e.cause }
func (diagnosticMulti) Error() string        { return "multiple" }
func (e diagnosticMulti) Unwrap() []error    { return e.children }
func (diagnosticPanicUnwrap) Error() string  { return "unreachable" }
func (diagnosticPanicUnwrap) Unwrap() error  { panic("traversed past diagnostic node budget") }

type diagnosticMulti struct{ children []error }
type diagnosticPanicUnwrap struct{}

func reportedDiagnosticCause(t *testing.T, err error) string {
	t.Helper()
	var output bytes.Buffer
	redactor := NewRedactor()
	logger := slog.New(NewJSONHandler(&output, diagnosticTestLevel(slog.LevelDebug), "daemon", redactor))
	NewDiagnosticReporter(logger, redactor)(context.Background(), diagnostics.Record{
		Component: "daemon.settings", Event: "operation_failed", Level: slog.LevelError, Err: err,
	})
	return jsonStringField(t, decodeDiagnosticRecord(t, output.Bytes()), "cause")
}

func diagnosticTestLevel(level slog.Level) *slog.LevelVar {
	var result slog.LevelVar
	result.Set(level)
	return &result
}

func decodeDiagnosticRecord(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode diagnostic JSON: %v: %q", err, raw)
	}
	return record
}

func jsonStringField(t *testing.T, record map[string]json.RawMessage, key string) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(record[key], &value); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	return value
}
