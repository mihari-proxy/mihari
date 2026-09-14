package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestDiagnosticOriginal_LongOuterRetainsLeafCause(t *testing.T) {
	err := &os.PathError{Op: strings.Repeat("x", diagnosticMaxBytes), Path: "fixture", Err: io.ErrUnexpectedEOF}
	text := diagnosticText(err, nil)
	if !strings.Contains(text, "unexpected EOF") || !strings.Contains(text, "[truncated]") || len(text) > diagnosticMaxBytes {
		t.Fatal("long outer context hid the leaf cause or exceeded the budget")
	}
}

func TestDiagnosticOriginal_APIErrorRetainsDetails(t *testing.T) {
	err := protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid fixture", Details: map[string]any{"field": "config.yaml:42", "received": "token=fixture", "count": 7}}
	text := diagnosticText(err, nil)
	for _, want := range []string{"invalid fixture", "config.yaml:42", "token=fixture", "count"} {
		if !strings.Contains(text, want) {
			t.Fatalf("API diagnostic detail missing: %s", want)
		}
	}
}

func TestRecordFragment_ErrorAttributeDoesNotRequireCauseKey(t *testing.T) {
	for _, key := range []string{"error", "err", "download_failure"} {
		t.Run(key, func(t *testing.T) {
			var out bytes.Buffer
			raw := strings.Repeat("\x01", diagnosticMaxBytes)
			logger := slog.New(NewJSONHandler(&out, &slog.LevelVar{}, "daemon", nil)).WithGroup("request")
			logger.Error("operation failed", key, errors.New(raw))
			records := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'})
			if len(records) < 2 {
				t.Fatal("long error attribute was dropped instead of fragmented")
			}
			var restored strings.Builder
			for _, line := range records {
				if len(line)+1 > MaxExportRecordBytes {
					t.Fatal("physical record too large")
				}
				var record map[string]json.RawMessage
				if err := json.Unmarshal(line, &record); err != nil {
					t.Fatal(err)
				}
				var attrs map[string]string
				if err := json.Unmarshal(record["request"], &attrs); err != nil {
					t.Fatal(err)
				}
				restored.WriteString(attrs[key])
			}
			if restored.String() != raw {
				t.Fatal("error attribute original text lost")
			}
		})
	}
}

func TestRecordFragment_MultipleLargeErrorsRemainComplete(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(NewJSONHandler(&out, &slog.LevelVar{}, "daemon", nil)).WithGroup("request")
	first, second := strings.Repeat("\x01", diagnosticMaxBytes), strings.Repeat("\x02", diagnosticMaxBytes)
	logger.Error("operation failed", "error", errors.New(first), "cleanup", errors.New(second))
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'})
	if len(lines) < 2 {
		t.Fatal("multiple large errors lost the whole log record")
	}
	var restoredFirst, restoredSecond strings.Builder
	var identity string
	for index, line := range lines {
		var record struct {
			ID      string            `json:"record_id"`
			Index   int               `json:"fragment_index"`
			Count   int               `json:"fragment_count"`
			Request map[string]string `json:"request"`
		}
		if len(line)+1 > MaxExportRecordBytes {
			t.Fatal("physical record exceeds export budget")
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			identity = record.ID
		}
		if identity == "" || record.ID != identity || record.Index != index+1 || record.Count != len(lines) {
			t.Fatal("fragment identity or ordering lost")
		}
		restoredFirst.WriteString(record.Request["error"])
		restoredSecond.WriteString(record.Request["cleanup"])
	}
	if restoredFirst.String() != first || restoredSecond.String() != second {
		t.Fatal("independent error text lost")
	}
}
