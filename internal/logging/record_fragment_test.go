package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRecordFragment_EscapedDiagnosticSurvivesExportBudget(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewJSONHandler(&output, &slog.LevelVar{}, "daemon", nil))
	raw := strings.Repeat("\x01", 256<<10)
	logger.ErrorContext(WithOperation(context.Background(), OperationMetadata{ID: "op-fragment", Name: "settings.update"}), "save.failed", "cause", diagnosticText(errors.New(raw), nil))
	lines := bytes.Split(bytes.TrimSuffix(output.Bytes(), []byte("\n")), []byte("\n"))
	if len(lines) < 2 {
		t.Fatal("escaped diagnostic needs multiple bounded physical records")
	}
	var restored strings.Builder
	var identity string
	for index, line := range lines {
		if len(line)+1 > MaxExportRecordBytes {
			t.Fatal("file record cannot pass the existing export reader")
		}
		var record struct {
			Cause       string `json:"cause"`
			RecordID    string `json:"record_id"`
			Index       int    `json:"fragment_index"`
			Count       int    `json:"fragment_count"`
			OperationID string `json:"operation_id"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			identity = record.RecordID
		}
		if identity == "" || identity != record.RecordID || record.Index != index+1 || record.Count != len(lines) || record.OperationID != "op-fragment" {
			t.Fatal("fragment identity or ordering was lost")
		}
		restored.WriteString(record.Cause)
	}
	if restored.String() != raw {
		t.Fatal("fragmented original diagnostic is incomplete")
	}
}

func TestRecordFragment_CoreRetainsLongLogicalLine(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewJSONHandler(&output, &slog.LevelVar{}, "mihomo", nil))
	capture := NewLineCaptureWriter(logger, slog.LevelInfo, "stderr")
	raw := strings.Repeat("core failure context ", 8000)
	if _, err := capture.Write([]byte(raw + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := capture.Close(); err != nil {
		t.Fatal(err)
	}
	if got := jsonStringField(t, decodeDiagnosticRecord(t, output.Bytes()), "msg"); got != raw {
		t.Fatal("core output was truncated below 256 KiB")
	}
}

func TestLineCapture_RepairAndTruncationStayWithinLogicalBudget(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		invalid   bool
	}{
		{"invalid bytes", strings.Repeat("\xff", MaxCaptureLineBytes), true},
		{"multibyte boundary", strings.Repeat("界", MaxCaptureLineBytes/3+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			capture := NewLineCaptureWriter(slog.New(NewJSONHandler(&output, &slog.LevelVar{}, "mihomo", nil)), slog.LevelInfo, "stderr")
			if _, err := capture.Write([]byte(tc.raw + "\n")); err != nil {
				t.Fatal(err)
			}
			if err := capture.Close(); err != nil {
				t.Fatal(err)
			}
			record := decodeDiagnosticRecord(t, output.Bytes())
			msg := jsonStringField(t, record, "msg")
			if len(msg) > MaxCaptureLineBytes || !utf8.ValidString(msg) || string(record["truncated"]) != "true" {
				t.Fatal("UTF-8 repair or truncation exceeded the logical budget")
			}
			if got := string(record["invalid_utf8"]) == "true"; got != tc.invalid {
				t.Fatal("a valid multibyte boundary was classified as invalid input")
			}
		})
	}
}
