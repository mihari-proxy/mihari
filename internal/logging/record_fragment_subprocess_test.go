package logging

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordFragment_TwoProcessesKeepOriginalContentAcrossRotation(t *testing.T) {
	fs, paths := openTestLogFS(t)
	cfg := Config{Level: slog.LevelInfo, MaxSizeBytes: 1 << 20, MaxFiles: 10}
	children := []*rotatorChild{
		startRotatorChild(t, paths.Root, paths.TUILog, "fragment-open-pause", "A", cfg, 1),
		startRotatorChild(t, paths.Root, paths.TUILog, "fragment-open-pause", "B", cfg, 1),
	}
	for _, child := range children {
		requireRotatorChildLine(t, child, "ready")
	}
	for _, child := range children {
		if _, err := io.WriteString(child.stdin, "open\n"); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range children {
		requireRotatorChildLine(t, child, "opened")
	}
	for _, child := range children {
		if _, err := io.WriteString(child.stdin, "go\n"); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range children {
		waitChildExit(t, child)
	}
	base := filepath.Base(paths.TUILog)
	if len(collectLogFiles(t, fs, paths.LogDir, base)) < 2 {
		t.Fatal("long records did not exercise rotation")
	}
	groups := map[string][]string{}
	writers := map[string]string{}
	for _, line := range readAllJSONL(t, fs, paths.LogDir, base) {
		var record struct {
			ID     string `json:"record_id"`
			Index  int    `json:"fragment_index"`
			Count  int    `json:"fragment_count"`
			Cause  string `json:"cause"`
			Writer string `json:"writer"`
		}
		if len(line)+1 > MaxExportRecordBytes {
			t.Fatal("fragment exceeds reader budget")
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record.ID == "" || record.Count < 2 || record.Index < 1 || record.Index > record.Count {
			t.Fatal("invalid fragment metadata")
		}
		if groups[record.ID] == nil {
			groups[record.ID] = make([]string, record.Count)
			writers[record.ID] = record.Writer
		}
		if writers[record.ID] != record.Writer || groups[record.ID][record.Index-1] != "" {
			t.Fatal("process records mixed or duplicated")
		}
		groups[record.ID][record.Index-1] = record.Cause
	}
	if len(groups) != 2 {
		t.Fatalf("logical records=%d want=2", len(groups))
	}
	for _, parts := range groups {
		if strings.Join(parts, "") != strings.Repeat("\x01", diagnosticMaxBytes) {
			t.Fatal("original content lost across processes/rotation")
		}
	}
}

func TestRecordFragment_SubprocessWritePreservesFailure(t *testing.T) {
	cause := errors.New("fixture fragment write failure")
	out := &fragmentFailureWriter{failAt: 2, failure: cause}
	if err := writeRotatorFragment(out, "fixture"); !errors.Is(err, cause) {
		t.Fatalf("fragment write error=%v, want original failure", err)
	}
	if out.writes != 2 {
		t.Fatalf("writes=%d, writer continued after failure", out.writes)
	}
}
