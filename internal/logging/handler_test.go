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

type sensitiveGroupLogValuer struct {
	value  string
	nested string
}

func (v sensitiveGroupLogValuer) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("value", v.value),
		slog.Group("detail", slog.String("note", v.nested)),
	)
}

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

func TestJSONHandler_SensitiveParentGroupsReplaceWholeValue(t *testing.T) {
	tests := []struct {
		name   string
		parent string
		attr   slog.Attr
	}{
		{
			name:   "group",
			parent: "token",
			attr: slog.Group("token",
				slog.String("value", "hunter-two"),
				slog.Group("detail", slog.String("note", "nested-hunter-two")),
			),
		},
		{
			name:   "group LogValuer",
			parent: "credential",
			attr: slog.Any("credential", sensitiveGroupLogValuer{
				value:  "valuer-hunter-two",
				nested: "valuer-nested-hunter-two",
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			level := &slog.LevelVar{}
			level.Set(slog.LevelDebug)
			handler := NewJSONHandler(&buf, level, "tui", NewRedactor())
			record := slog.NewRecord(time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC), slog.LevelInfo, "group test", 0)
			record.AddAttrs(test.attr)
			if err := handler.Handle(context.Background(), record); err != nil {
				t.Fatalf("Handle: %v", err)
			}

			encoded := buf.String()
			assertNotContains(t, encoded, "hunter-two", "nested-hunter-two", "valuer-hunter-two", "valuer-nested-hunter-two")
			var payload map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload); err != nil {
				t.Fatalf("decode JSON: %v in %q", err, encoded)
			}
			if got := payload[test.parent]; got != "***" {
				t.Fatalf("%s = %#v, want whole sensitive value replaced with ***", test.parent, got)
			}
		})
	}
}

func TestJSONHandler_ComponentOverrideEmitsOneKey(t *testing.T) {
	tests := []struct {
		name        string
		log         func(*slog.Logger)
		want        string
		wantRequest bool
	}{
		{name: "record attr", log: func(logger *slog.Logger) { logger.Info("component test", "component", "record-override") }, want: "record-override"},
		{name: "With attr", log: func(logger *slog.Logger) { logger.With("component", "with-override").Info("component test") }, want: "with-override"},
		{name: "record inline group", log: func(logger *slog.Logger) {
			logger.Info("component test", slog.Group("", slog.String("component", "record-inline"), slog.String("request", "kept")))
		}, want: "record-inline", wantRequest: true},
		{name: "With inline group", log: func(logger *slog.Logger) {
			logger.With(slog.Group("", slog.String("component", "with-inline"), slog.String("request", "kept"))).Info("component test")
		}, want: "with-inline", wantRequest: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			level := &slog.LevelVar{}
			level.Set(slog.LevelDebug)
			logger := slog.New(NewJSONHandler(&buf, level, "daemon", NewRedactor()))

			test.log(logger)

			encoded := buf.String()
			if got := strings.Count(encoded, `"component":`); got != 1 {
				t.Fatalf("component key count = %d, want 1 in %s", got, encoded)
			}
			var payload map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload); err != nil {
				t.Fatalf("decode JSON: %v in %q", err, encoded)
			}
			if got := payload["component"]; got != test.want {
				t.Fatalf("component = %#v, want %s", got, test.want)
			}
			if test.wantRequest && payload["request"] != "kept" {
				t.Fatalf("request = %#v, want kept", payload["request"])
			}
		})
	}
}

func TestJSONHandler_WithGroupKeepsComponentAtTopLevel(t *testing.T) {
	var buf bytes.Buffer
	level := &slog.LevelVar{}
	level.Set(slog.LevelDebug)
	logger := slog.New(NewJSONHandler(&buf, level, "daemon", NewRedactor())).WithGroup("request")

	logger.Info("grouped", "method", "GET")

	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload); err != nil {
		t.Fatalf("decode JSON: %v in %q", err, buf.String())
	}
	if got := payload["component"]; got != "daemon" {
		t.Fatalf("top-level component = %#v, want daemon", got)
	}
	group, ok := payload["request"].(map[string]any)
	if !ok || group["method"] != "GET" {
		t.Fatalf("request group = %#v, want grouped method", payload["request"])
	}
	if _, nested := group["component"]; nested {
		t.Fatalf("component was nested inside request: %#v", group)
	}
}
