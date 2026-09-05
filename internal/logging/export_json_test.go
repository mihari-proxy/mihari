package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExportJSON_BoundedPhysicalLines(t *testing.T) {
	stamp := "2026-09-02T12:00:00Z"
	valid := `{"time":"` + stamp + `","msg":"ok"}`

	t.Run("exact limit and final line without newline", func(t *testing.T) {
		padding := MaxExportRecordBytes - len(`{"time":"`+stamp+`","msg":""}`)
		line := `{"time":"` + stamp + `","msg":"` + strings.Repeat("x", padding) + `"}`
		var out bytes.Buffer
		stats, err := exportJSON(context.Background(), strings.NewReader(line), &out, ExportRange{Kind: RangeAll}, NewRedactor())
		if err != nil || stats.Lines != 1 || stats.SkippedInvalid != 0 {
			t.Fatalf("exportJSON exact limit = %+v, %v", stats, err)
		}
	})

	t.Run("overlong fragmented line is discarded once and next line survives", func(t *testing.T) {
		input := strings.Repeat("x", MaxExportRecordBytes+1) + "\n" + valid + "\n"
		var out bytes.Buffer
		stats, err := exportJSON(context.Background(), strings.NewReader(input), &out, ExportRange{Kind: RangeAll}, NewRedactor())
		if err != nil || stats.Lines != 1 || stats.SkippedInvalid != 1 {
			t.Fatalf("exportJSON overlong = %+v, %v", stats, err)
		}
		if strings.Count(out.String(), "\n") != 1 {
			t.Fatalf("output = %q, want one line", out.String())
		}
	})

	t.Run("reader storage remains bounded", func(t *testing.T) {
		reader := newBoundedLineReader(strings.NewReader(strings.Repeat("x", 4*MaxExportRecordBytes)))
		for {
			_, _, err := reader.Next(context.Background())
			if errors.Is(err, errExportLineTooLong) {
				continue
			}
			if err != nil {
				break
			}
		}
		if cap(reader.line) > MaxExportRecordBytes+1 {
			t.Fatalf("retained line capacity = %d, want <= %d", cap(reader.line), MaxExportRecordBytes+1)
		}
		if reader.reader.Size() != exportReadBufferBytes {
			t.Fatalf("reader buffer size = %d, want %d", reader.reader.Size(), exportReadBufferBytes)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := exportJSON(ctx, strings.NewReader(valid), &bytes.Buffer{}, ExportRange{Kind: RangeAll}, NewRedactor())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
}

func TestExportJSON_StrictDecodeTimeRangeNumberAndOrder(t *testing.T) {
	input := strings.Join([]string{
		`{"time":"2026-09-02T10:00:00Z","seq":9007199254740993}`,
		`{"time":"2026-09-02T12:00:00Z","seq":2}`,
		`{"time":"2026-09-02T11:00:00Z","seq":3}`,
		`{"time":"2026-09-02T13:00:00Z","ignored":"outside"}`,
		`{"time":"2026-09-02T09:59:59Z" broken}`,
		`{broken}`,
		`{"msg":"missing"}`,
		`{"time":"yesterday"}`,
		`[1,2]`,
		`42`,
		`{"time":"2026-09-02T10:30:00Z"} {"time":"2026-09-02T10:31:00Z"}`,
		`{"time":"2026-09-02T10:30:00Z"} trailing`,
		"{\"time\":\"2026-09-02T10:30:00Z\"} \t ",
	}, "\n")
	window := ExportRange{Kind: RangeBetween, From: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)}
	var out bytes.Buffer
	stats, err := exportJSON(context.Background(), strings.NewReader(input), &out, window, NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Lines != 4 || stats.SkippedInvalid != 8 {
		t.Fatalf("stats = %+v, want lines=4 invalid=8", stats)
	}
	if !strings.Contains(out.String(), `9007199254740993`) {
		t.Fatalf("large integer changed: %s", out.String())
	}
	var seqs []json.Number
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var record map[string]any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if seq, ok := record["seq"].(json.Number); ok {
			seqs = append(seqs, seq)
		}
	}
	if got := strings.Join([]string{seqs[0].String(), seqs[1].String(), seqs[2].String()}, ","); got != "9007199254740993,2,3" {
		t.Fatalf("record order = %s", got)
	}
}

func TestExportJSON_RedactedCountsRecordsNotReplacements(t *testing.T) {
	input := `{"time":"2026-09-02T10:00:00Z","token":"hidden","msg":"https://example.test/private"}`
	var out bytes.Buffer
	stats, err := exportJSON(context.Background(), strings.NewReader(input), &out, ExportRange{Kind: RangeAll}, NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Lines != 1 || stats.Redacted != 1 {
		t.Fatalf("stats = %+v, want one line and one redacted record", stats)
	}
	if strings.Contains(out.String(), "hidden") || strings.Contains(out.String(), "example.test") {
		t.Fatalf("output leaked sensitive values: %s", out.String())
	}
}

func TestExportJSON_ManifestValues(t *testing.T) {
	now := time.Date(2026, 9, 2, 23, 41, 8, 123456789, time.FixedZone("local", 8*60*60))
	all := newExportManifest(now, ExportRange{Kind: RangeAll}, nil)
	encoded, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"from"`) || strings.Contains(string(encoded), `"to"`) {
		t.Fatalf("all manifest includes bounds: %s", encoded)
	}
	if all.Schema != "mihari-logs-export/v1" || all.ExportedAt != "2026-09-02T23:41:08.123456789+08:00" || all.Timezone != "+08:00" {
		t.Fatalf("manifest = %+v", all)
	}
	if len(all.Notes) != 1 || all.Notes[0] != exportReviewNote {
		t.Fatalf("notes = %#v", all.Notes)
	}

	rangeValue := ExportRange{Kind: RangeBetween, From: time.Date(2026, 9, 2, 10, 0, 0, 1, time.UTC), To: time.Date(2026, 9, 2, 11, 0, 0, 2, time.UTC)}
	bounded := newExportManifest(now, rangeValue, []exportFile{{Name: "daemon/mihari-daemon.log", Sources: []string{"mihari-daemon.log"}}})
	if bounded.Range.From != "2026-09-02T10:00:00.000000001Z" || bounded.Range.To != "2026-09-02T11:00:00.000000002Z" {
		t.Fatalf("range = %+v", bounded.Range)
	}
	if strings.Contains(string(mustJSON(t, bounded)), `C:\`) || strings.Contains(string(mustJSON(t, bounded)), ".lock") {
		t.Fatalf("manifest leaked forbidden paths: %s", mustJSON(t, bounded))
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
