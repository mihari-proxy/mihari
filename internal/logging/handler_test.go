package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestJSONHandler_FormatAndComponent(t *testing.T) {
	fixed := time.Date(2026, 9, 2, 23, 41, 8, 123456789, time.FixedZone("CST", 8*3600))
	redactor := NewRedactor(testWebCredential)

	components := []string{"daemon", "tui", "mihomo", "daemon.subscription"}
	for _, component := range components {
		t.Run(component, func(t *testing.T) {
			var buf bytes.Buffer
			level := &slog.LevelVar{}
			level.Set(slog.LevelDebug)
			handler := NewJSONHandler(&buf, level, component, redactor)

			record := slog.NewRecord(fixed, slog.LevelInfo, "hello "+testWebCredential, 0)
			record.AddAttrs(
				slog.String("token", "should-hide"),
				slog.String("url", "https://example.test/path"),
				slog.Group("nested", slog.String("password", "nested-pass")),
			)
			if err := handler.Handle(context.Background(), record); err != nil {
				t.Fatalf("Handle: %v", err)
			}

			line := buf.String()
			if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") {
				t.Fatalf("want single JSON line with trailing newline, got %q", line)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &payload); err != nil {
				t.Fatalf("json: %v in %q", err, line)
			}
			for _, key := range []string{"time", "level", "msg", "component"} {
				if _, ok := payload[key]; !ok {
					t.Fatalf("missing %q in %v", key, payload)
				}
			}
			if payload["component"] != component {
				t.Fatalf("component = %v, want %s", payload["component"], component)
			}
			if _, ok := payload["source"]; ok {
				t.Fatalf("source key must not appear: %v", payload)
			}
			gotTime, _ := payload["time"].(string)
			if gotTime != "2026-09-02T23:41:08.123456789+08:00" {
				t.Fatalf("time = %q, want RFC3339Nano with numeric offset", gotTime)
			}
			if payload["level"] != "INFO" {
				t.Fatalf("level = %v", payload["level"])
			}
			msg, _ := payload["msg"].(string)
			if msg != "hello ***" {
				t.Fatalf("msg = %q", msg)
			}
			encoded := line
			assertNotContains(t, encoded, testWebCredential, "should-hide", "nested-pass", "example.test")
			if !strings.Contains(encoded, `"token":"***"`) {
				t.Fatalf("token attr not redacted: %s", encoded)
			}
			if !strings.Contains(encoded, `[REDACTED_URL]`) {
				t.Fatalf("url attr not redacted: %s", encoded)
			}
		})
	}
}

func TestJSONHandler_LevelVarFilters(t *testing.T) {
	var buf bytes.Buffer
	level := &slog.LevelVar{}
	level.Set(slog.LevelWarn)
	handler := NewJSONHandler(&buf, level, "daemon", NewRedactor())
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info must be disabled when level=warn")
	}
	if !handler.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("error must be enabled when level=warn")
	}
}
