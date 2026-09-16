package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestMihomoCapture_RealLevelAndOriginalMessage(t *testing.T) {
	for _, test := range []struct {
		name, raw           string
		fallback, threshold slog.Level
		want                string
	}{
		{"stdout warning", `time="2026-09-16T12:00:00Z" level=warning msg="warning"`, slog.LevelInfo, slog.LevelWarn, "WARN"},
		{"debug on stderr", `level=debug msg="details"`, slog.LevelWarn, slog.LevelDebug, "DEBUG"},
		{"filtered debug", `level=debug msg="details"`, slog.LevelWarn, slog.LevelInfo, ""},
		{"json", `{"level":"error","msg":"token=synthetic-only"}`, slog.LevelInfo, slog.LevelError, "ERROR"},
		{"ansi", "\x1b[31mlevel=error\x1b[0m msg=raw", slog.LevelInfo, slog.LevelError, "ERROR"},
		{"message imitation", `time="now" level=info msg="level=error"`, slog.LevelWarn, slog.LevelWarn, ""},
		{"nested json", `{"msg":{"level":"error"}}`, slog.LevelInfo, slog.LevelInfo, "INFO"},
		{"plain word", `some error text`, slog.LevelWarn, slog.LevelInfo, "WARN"},
		{"fatal", `level=fatal msg=failed`, slog.LevelInfo, slog.LevelError, "ERROR"},
		{"unknown", `level=other msg=failed`, slog.LevelWarn, slog.LevelInfo, "WARN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			var threshold slog.LevelVar
			threshold.Set(test.threshold)
			capture := NewMihomoCaptureWriter(slog.New(NewJSONHandler(&out, &threshold, "mihomo", nil)), test.fallback, "stdout")
			// Deliberate chunk boundary through the severity field.
			for _, chunk := range []string{test.raw[:3], test.raw[3:] + "\r\n"} {
				if _, err := capture.Write([]byte(chunk)); err != nil {
					t.Fatal(err)
				}
			}
			if err := capture.Close(); err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if out.Len() != 0 {
					t.Fatalf("unexpected record %s", &out)
				}
				return
			}
			var record struct {
				Level string
				Msg   string
			}
			if err := json.Unmarshal(out.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record.Level != test.want || record.Msg != test.raw {
				t.Fatalf("record=%+v", record)
			}
		})
	}
}

func TestMihomoCapture_TruncatedJSONRetainsVisibleTopLevelSeverity(t *testing.T) {
	var out bytes.Buffer
	var threshold slog.LevelVar
	threshold.Set(slog.LevelWarn)
	capture := NewMihomoCaptureWriter(slog.New(NewJSONHandler(&out, &threshold, "mihomo", nil)), slog.LevelInfo, "stdout")
	raw := `{"level":"error","msg":"` + strings.Repeat("x", MaxCaptureLineBytes) + `"}`
	if _, err := capture.Write([]byte(raw + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := capture.Close(); err != nil {
		t.Fatal(err)
	}
	var record struct {
		Level     string
		Msg       string
		Truncated bool
	}
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record.Level != "ERROR" || !record.Truncated || record.Msg != raw[:MaxCaptureLineBytes] {
		t.Fatal("truncation lost original text or known severity")
	}
}

func TestMihomoCapture_TruncatedJSONDoesNotAdoptMessageOrNestedSeverity(t *testing.T) {
	for _, prefix := range []string{
		`{"msg":{"level":"error"},"padding":"`,
		`{"msg":"level=error`,
		`{"msg":"fixture","level":42,"padding":"`,
		`{"msg":"fixture","level":"err`,
	} {
		t.Run(prefix, func(t *testing.T) {
			var out bytes.Buffer
			threshold := new(slog.LevelVar)
			threshold.Set(slog.LevelWarn)
			capture := NewMihomoCaptureWriter(slog.New(NewJSONHandler(&out, threshold, "mihomo", nil)), slog.LevelInfo, "stdout")
			if _, err := capture.Write([]byte(prefix + strings.Repeat("x", MaxCaptureLineBytes) + "\n")); err != nil {
				t.Fatal(err)
			}
			if err := capture.Close(); err != nil {
				t.Fatal(err)
			}
			if out.Len() != 0 {
				t.Fatal("truncated JSON inferred error from incomplete or nested field")
			}
		})
	}
}
