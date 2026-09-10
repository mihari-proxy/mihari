package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func decodeOperationRecord(t *testing.T, data string) map[string]json.RawMessage {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		t.Fatal("expected JSON object")
	}
	out := make(map[string]json.RawMessage)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			t.Fatal("invalid JSON key")
		}
		key, ok := token.(string)
		if !ok {
			t.Fatal("expected string key")
		}
		if _, exists := out[key]; exists {
			t.Fatalf("duplicate top-level key %q", key)
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			t.Fatal("invalid JSON value")
		}
		out[key] = value
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		t.Fatal("unterminated JSON object")
	}
	if _, err := d.Token(); err != io.EOF {
		t.Fatal("unexpected trailing JSON")
	}
	return out
}

func TestOperationHandler_ContextWinsAtRoot(t *testing.T) {
	for _, group := range []string{"", "request", "token", "operation_id", "operation"} {
		t.Run("group="+group, func(t *testing.T) {
			var buf bytes.Buffer
			base := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", NewRedactor()))
			logger := base.With("operation_id", "stale", "operation", "stale-name", "keep", true)
			if group != "" {
				logger = logger.WithGroup(group)
			}
			ctx := WithOperation(context.Background(), OperationMetadata{ID: "op-a", Name: "settings.update"})
			logger.InfoContext(ctx, "done", "operation_id", "record-old", "operation", "record-name", "value", 7)
			out := decodeOperationRecord(t, buf.String())
			if string(out["operation_id"]) != `"op-a"` || string(out["operation"]) != `"settings.update"` {
				t.Fatal("ctx fields must be authoritative at root")
			}
			if string(out["component"]) != `"daemon"` || string(out["keep"]) != "true" {
				t.Fatal("existing root fields must survive")
			}
			if group == "request" {
				var nested map[string]json.RawMessage
				if err := json.Unmarshal(out[group], &nested); err != nil {
					t.Fatal(err)
				}
				if string(nested["operation_id"]) != `"record-old"` || string(nested["value"]) != "7" {
					t.Fatal("ordinary nested fields must survive")
				}
			}
		})
	}
}

func TestOperationHandler_IDFilteringAndRedaction(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{"ordinary", "op-1_A.b:c", `"op-1_A.b:c"`},
		{"max", strings.Repeat("a", 128), `"` + strings.Repeat("a", 128) + `"`},
		{"empty", "", ""},
		{"long", strings.Repeat("a", 129), ""},
		{"space", "bad id", ""},
		{"unicode", "操作", ""},
		{"control", "bad\nvalue", ""},
		{"url", "https://invalid.test/x", ""},
		{"secret", "registered-secret", `"***"`},
		{"hex-secret", strings.Repeat("b", 64), `"***"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := NewRedactor("registered-secret")
			logger := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", r)).With("operation_id", "stale")
			ctx := WithOperation(context.Background(), OperationMetadata{ID: test.id, Name: "registered-secret"})
			logger.InfoContext(ctx, "done")
			out := decodeOperationRecord(t, buf.String())
			if string(out["operation_id"]) != test.want {
				t.Fatal("unexpected ID output")
			}
			if string(out["operation"]) != `"***"` {
				t.Fatal("operation must be redacted")
			}
			if got, _ := OperationFromContext(ctx); got.ID != test.id {
				t.Fatal("input changed")
			}
		})
	}
}

func TestOperationHandler_NoContextCompatibility(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", NewRedactor())).With("operation_id", "legacy")
	logger.Info("legacy")
	out := decodeOperationRecord(t, buf.String())
	if string(out["operation_id"]) != `"legacy"` {
		t.Fatal("legacy With behavior changed")
	}
	buf.Reset()
	logger.InfoContext(WithOperation(context.Background(), OperationMetadata{}), "empty")
	out = decodeOperationRecord(t, buf.String())
	if _, exists := out["operation_id"]; exists {
		t.Fatal("empty binding restored stale ID")
	}
	if _, exists := out["operation"]; exists {
		t.Fatal("empty binding emitted a name")
	}
}

func TestOperationHandler_InlineGroupsAndRecordUnchanged(t *testing.T) {
	var buf bytes.Buffer
	handler := NewJSONHandler(&buf, new(slog.LevelVar), "daemon", NewRedactor())
	handler = handler.WithAttrs([]slog.Attr{slog.Group("",
		slog.String("operation_id", "stale"), slog.String("keep", "yes"))})
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	record.AddAttrs(slog.Group("", slog.Group("", slog.String("operation_id", "spoof")),
		slog.String("component", "daemon.settings"), slog.Int("keep_record", 1)))
	for i := 0; i < 10; i++ {
		record.AddAttrs(slog.Int(fmt.Sprintf("n%d", i), i))
	}
	original := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(a slog.Attr) bool { original = append(original, a); return true })
	ctx := WithOperation(context.Background(), OperationMetadata{ID: "op-a"})
	if err := handler.Handle(ctx, record); err != nil {
		t.Fatal(err)
	}
	out := decodeOperationRecord(t, buf.String())
	if string(out["operation_id"]) != `"op-a"` || string(out["keep"]) != `"yes"` ||
		string(out["component"]) != `"daemon.settings"` || string(out["keep_record"]) != "1" {
		t.Fatal("inline group filtering damaged fields")
	}
	after := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(a slog.Attr) bool { after = append(after, a); return true })
	if !reflect.DeepEqual(original, after) {
		t.Fatal("caller record mutated")
	}
	buf.Reset()
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "spoof") {
		t.Fatal("operation handling mutated reusable attributes")
	}
}

func TestOperationHandler_DerivedLoggerAndDynamicSecrets(t *testing.T) {
	var buf bytes.Buffer
	r := NewRedactor()
	logger := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", r))
	derived := logger.With("keep", true).WithGroup("request").With("stage", "send").WithGroup("detail")
	r.ReplaceExact([]string{"later-secret"})
	ctx := WithOperation(context.Background(), OperationMetadata{ID: "later-secret", Name: "settings.update"})
	derived.InfoContext(ctx, "done", "password", "hidden-pass", "value", 1)
	out := decodeOperationRecord(t, buf.String())
	if string(out["operation_id"]) != `"***"` {
		t.Fatal("derived handler missed current redaction rules")
	}
	if strings.Contains(buf.String(), "later-secret") || strings.Contains(buf.String(), "hidden-pass") {
		t.Fatal("sensitive data leaked")
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(out["request"], &request); err != nil {
		t.Fatal(err)
	}
	if string(request["stage"]) != `"send"` {
		t.Fatal("derived attributes lost")
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(request["detail"], &detail); err != nil {
		t.Fatal(err)
	}
	if string(detail["value"]) != "1" {
		t.Fatal("nested data lost")
	}
	buf.Reset()
	logger.Info("no ctx")
	out = decodeOperationRecord(t, buf.String())
	if _, exists := out["operation_id"]; exists {
		t.Fatal("previous operation leaked to base logger")
	}
}

func TestOperationHandler_CancellationAndLevel(t *testing.T) {
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelWarn)
	logger := slog.New(NewJSONHandler(&buf, level, "daemon", NewRedactor()))
	ctx, cancel := context.WithCancel(WithOperation(context.Background(), OperationMetadata{ID: "op-cancel"}))
	cancel()
	logger.InfoContext(ctx, "filtered")
	if buf.Len() != 0 {
		t.Fatal("operation bypassed level filtering")
	}
	logger.ErrorContext(ctx, "must survive cancellation")
	out := decodeOperationRecord(t, buf.String())
	if string(out["operation_id"]) != `"op-cancel"` {
		t.Fatal("canceled ctx lost diagnostic")
	}
}

func TestOperationHandler_ConcurrentIsolation(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, new(slog.LevelVar), "daemon", NewRedactor()))
	const count = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("op-%d", i)
			ctx := WithOperation(context.Background(), OperationMetadata{ID: id, Name: "settings.update"})
			child := logger.With("operation_id", "stale").WithGroup("request")
			<-start
			child.InfoContext(ctx, "completed", "expected", id)
		}(i)
	}
	close(start)
	wg.Wait()
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != count {
		t.Fatal("missing or interleaved log records")
	}
	seen := make(map[string]bool)
	for _, line := range lines {
		out := decodeOperationRecord(t, line)
		var request map[string]json.RawMessage
		if err := json.Unmarshal(out["request"], &request); err != nil {
			t.Fatal(err)
		}
		key := string(out["operation_id"])
		if key != string(request["expected"]) || seen[key] {
			t.Fatal("operation IDs crossed or duplicated")
		}
		seen[key] = true
	}
}
