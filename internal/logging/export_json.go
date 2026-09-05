package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	exportReadBufferBytes = 32 << 10
	exportReviewNote      = "mihomo level is Mihari capture classification; core-emitted node names and traffic metadata may remain"
)

var errExportLineTooLong = errors.New("export log line exceeds limit")

type exportFile struct {
	Name           string   `json:"name"`
	Lines          int64    `json:"lines"`
	SkippedInvalid int64    `json:"skipped_invalid"`
	Redacted       int64    `json:"redacted"`
	Sources        []string `json:"sources"`
}

type manifestRange struct {
	Kind string `json:"kind"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type exportManifest struct {
	Schema     string        `json:"schema"`
	ExportedAt string        `json:"exported_at"`
	Timezone   string        `json:"timezone"`
	Range      manifestRange `json:"range"`
	Files      []exportFile  `json:"files"`
	Notes      []string      `json:"notes"`
}

type boundedLineReader struct {
	reader *bufio.Reader
	line   []byte
}

func newBoundedLineReader(reader io.Reader) *boundedLineReader {
	return &boundedLineReader{
		reader: bufio.NewReaderSize(reader, exportReadBufferBytes),
		line:   make([]byte, 0, MaxExportRecordBytes+1),
	}
}

// Next returns one physical line without its trailing newline. An oversized
// line is fully discarded and reported exactly once.
func (r *boundedLineReader) Next(ctx context.Context) ([]byte, bool, error) {
	r.line = r.line[:0]
	overlong := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		fragment, readErr := r.reader.ReadSlice('\n')
		hasNewline := len(fragment) != 0 && fragment[len(fragment)-1] == '\n'
		if hasNewline {
			fragment = fragment[:len(fragment)-1]
		}
		if !overlong {
			if len(r.line)+len(fragment) > MaxExportRecordBytes {
				overlong = true
				r.line = r.line[:0]
			} else {
				r.line = append(r.line, fragment...)
			}
		}

		switch {
		case hasNewline:
			if overlong {
				return nil, true, errExportLineTooLong
			}
			return r.line, true, nil
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF):
			if len(fragment) == 0 && len(r.line) == 0 && !overlong {
				return nil, false, io.EOF
			}
			if overlong {
				return nil, true, errExportLineTooLong
			}
			return r.line, true, nil
		case readErr != nil:
			return nil, false, readErr
		}
	}
}

func exportJSON(ctx context.Context, source io.Reader, destination io.Writer, exportRange ExportRange, redactor *Redactor) (exportFile, error) {
	return exportJSONWithCheckpoints(ctx, source, destination, exportRange, redactor, nil)
}

func exportJSONWithCheckpoints(ctx context.Context, source io.Reader, destination io.Writer, exportRange ExportRange, redactor *Redactor, checkpoint func(exportStage) error) (exportFile, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if redactor == nil {
		redactor = NewRedactor()
	}
	reader := newBoundedLineReader(source)
	var stats exportFile
	for {
		if checkpoint != nil {
			if err := checkpoint(stageReadBatch); err != nil {
				return stats, err
			}
		}
		line, present, err := reader.Next(ctx)
		if errors.Is(err, io.EOF) {
			return stats, nil
		}
		if errors.Is(err, errExportLineTooLong) {
			stats.SkippedInvalid++
			continue
		}
		if err != nil {
			return stats, fmt.Errorf("read export log: %w", err)
		}
		if !present {
			continue
		}
		if checkpoint != nil {
			if err := checkpoint(stageDecodeLine); err != nil {
				return stats, err
			}
		}
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		record, recordTime, valid := decodeExportRecord(line)
		if !valid {
			stats.SkippedInvalid++
			continue
		}
		if exportRange.Kind != RangeAll && (recordTime.Before(exportRange.From) || recordTime.After(exportRange.To)) {
			continue
		}
		clean, changed := redactor.Value(record)
		encoded, err := json.Marshal(clean)
		if err != nil {
			stats.SkippedInvalid++
			continue
		}
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if checkpoint != nil {
			if err := checkpoint(stageWriteSpool); err != nil {
				return stats, err
			}
		}
		if _, err := destination.Write(append(encoded, '\n')); err != nil {
			return stats, fmt.Errorf("write export log: %w", err)
		}
		stats.Lines++
		if changed {
			stats.Redacted++
		}
	}
}

func decodeExportRecord(line []byte) (map[string]any, time.Time, bool) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil || record == nil {
		return nil, time.Time{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, time.Time{}, false
	}
	stamp, ok := record["time"].(string)
	if !ok {
		return nil, time.Time{}, false
	}
	recordTime, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return nil, time.Time{}, false
	}
	return record, recordTime, true
}

func newExportManifest(now time.Time, exportRange ExportRange, files []exportFile) exportManifest {
	rangeValue := manifestRange{Kind: string(exportRange.Kind)}
	if exportRange.Kind != RangeAll {
		rangeValue.From = exportRange.From.UTC().Format(time.RFC3339Nano)
		rangeValue.To = exportRange.To.UTC().Format(time.RFC3339Nano)
	}
	return exportManifest{
		Schema:     "mihari-logs-export/v1",
		ExportedAt: now.Format(time.RFC3339Nano),
		Timezone:   now.Format("-07:00"),
		Range:      rangeValue,
		Files:      files,
		Notes:      []string{exportReviewNote},
	}
}
