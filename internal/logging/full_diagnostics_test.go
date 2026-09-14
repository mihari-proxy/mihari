package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestFullDiagnostics_PreservesOriginalCauseAndPublicError(t *testing.T) {
	const raw = "download https://example.test/sub?token=original-token\npassword: original-password\nC:\\Users\\tester\\settings.yaml"
	leaf := errors.New(raw)
	err := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download failed"}, fmt.Errorf("refresh subscription: %w", leaf))
	var output bytes.Buffer
	redactor := NewRedactor("original-token", "original-password")
	logger := slog.New(NewJSONHandler(&output, &slog.LevelVar{}, "daemon", redactor))
	NewDiagnosticReporter(logger, redactor)(context.Background(), diagnostics.Record{Component: "subscription", Event: "refresh.failed", Level: slog.LevelError, Err: err})
	got := jsonStringField(t, decodeDiagnosticRecord(t, output.Bytes()), "cause")
	if !strings.Contains(got, raw) || !strings.Contains(got, "refresh subscription") {
		t.Fatal("file diagnostic discarded original cause or wrapper context")
	}
	var public protocol.APIError
	if !errors.As(err, &public) || !errors.Is(err, leaf) || err.Error() != "download failed" {
		t.Fatal("internal cause or public error identity changed")
	}
	encoded, marshalErr := json.Marshal(public)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), "original-") || strings.Contains(string(encoded), "refresh subscription") {
		t.Fatal("public error contains internal diagnostic")
	}
}

func TestFullDiagnostics_RetainsLargeTextAndJoinedCauses(t *testing.T) {
	const budget = 256 << 10
	raw := strings.Repeat("x", budget)
	if got := diagnosticText(errors.New(raw), NewRedactor()); got != raw {
		t.Fatalf("diagnostic retained %d bytes, want %d", len(got), len(raw))
	}
	if got := diagnosticText(errors.New(raw+"tail"), nil); len(got) > budget || !strings.HasSuffix(got, " [truncated]") {
		t.Fatal("oversize diagnostic lacks a bounded truncation marker")
	}
	joined := errors.Join(fmt.Errorf("publish config: %w", &os.PathError{Op: "rename", Path: "/home/test user/config.yaml", Err: os.ErrPermission}), errors.New("rollback reload failed: invalid field mixed-port"))
	got := diagnosticText(joined, nil)
	for _, want := range []string{"publish config", "/home/test user/config.yaml", "permission denied", "rollback reload failed: invalid field mixed-port"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing diagnostic context %q", want)
		}
	}
}

func TestFullDiagnostics_InvalidBytesStillDiscloseTruncation(t *testing.T) {
	raw := strings.Repeat("\xff", diagnosticMaxBytes+100)
	got := diagnosticText(errors.New(raw), nil)
	if len(got) > diagnosticMaxBytes || !strings.HasSuffix(got, " [truncated]") || !strings.Contains(got, "[invalid UTF-8]") {
		t.Fatal("UTF-8 repair hid discarded input or exceeded the diagnostic budget")
	}
}

func TestFullDiagnostics_HandlerPreservesBoundAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewJSONHandler(&output, &slog.LevelVar{}, "tui", NewRedactor("registered-password")))
	logger.With("token", "original-token").WithGroup("request").Info("https://example.test/path", "password", "registered-password")
	record := decodeDiagnosticRecord(t, output.Bytes())
	if jsonStringField(t, record, "token") != "original-token" || jsonStringField(t, record, "msg") != "https://example.test/path" {
		t.Fatal("file handler redacted root fields")
	}
	var group map[string]string
	if err := json.Unmarshal(record["request"], &group); err != nil {
		t.Fatal(err)
	}
	if group["password"] != "registered-password" {
		t.Fatal("file handler redacted grouped fields")
	}
}
