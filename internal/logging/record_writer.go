package logging

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const fragmentTextBytes = 64 << 10

// Bound the number of independent diagnostic fields and the second encoded
// representation retained while validating fragments before any file write.
const maxFragmentRecordBytes = diagnosticMaxNodes*6*diagnosticMaxBytes + MaxExportRecordBytes

var errLogRecordTooLarge = errors.New("log record metadata exceeds the physical record limit")

// recordWriter preserves the existing snapshot wire limit even when escaping
// expands a bounded logical message. Each output is one complete JSONL record.
type recordWriter struct {
	out   io.Writer
	newID func() (string, error)
}

func newLogRecordID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (w *recordWriter) Write(p []byte) (int, error) {
	if len(p) <= MaxExportRecordBytes {
		return writeCompleteRecord(w.out, p)
	}
	parts, err := w.fragments(p)
	if err != nil {
		// Encoding/identity failures never reached the rotating writer. Use
		// its independent outlet; IO failures below are reported by it once.
		if owner, ok := w.out.(interface{ report(FailureClass, error) }); ok {
			owner.report(FailureWrite, err)
		}
		return 0, err
	}
	for _, part := range parts {
		if _, err := writeCompleteRecord(w.out, part); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func writeCompleteRecord(out io.Writer, p []byte) (int, error) {
	n, err := out.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func (w *recordWriter) fragments(p []byte) ([][]byte, error) {
	// The original record already exists in the encoder, but decoding it
	// must not create an arbitrarily large second object tree.
	if len(p) > maxFragmentRecordBytes {
		return nil, errLogRecordTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(p))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("decode log record: %w", err)
	}
	type fieldChunk struct {
		object    map[string]any
		key, text string
	}
	var chunks []fieldChunk
	truncated := false
	for fields := 0; ; fields++ {
		object, key, text := fragmentField(record, 0)
		if object == nil || len(text) <= fragmentTextBytes {
			break
		}
		if fields >= diagnosticMaxNodes {
			return nil, errLogRecordTooLarge
		}
		text = boundDiagnostic(text)
		truncated = truncated || strings.HasSuffix(text, " [truncated]")
		// Each large field is empty in other fields' fragments. Concatenating
		// a field in fragment order reconstructs it without duplicating data.
		object[key] = ""
		for len(text) > fragmentTextBytes {
			end := fragmentTextBytes
			for !utf8.RuneStart(text[end]) {
				end--
			}
			chunks = append(chunks, fieldChunk{object, key, text[:end]})
			text = text[end:]
		}
		chunks = append(chunks, fieldChunk{object, key, text})
	}
	if len(chunks) == 0 {
		return nil, errLogRecordTooLarge
	}
	newID := w.newID
	if newID == nil {
		newID = newLogRecordID
	}
	id, err := newID()
	if err != nil {
		return nil, fmt.Errorf("create log record identity: %w", err)
	}
	if id == "" {
		return nil, errors.New("log record identity is empty")
	}
	record["record_id"] = id
	record["fragment_count"] = len(chunks)
	if _, exists := record["truncated"]; !exists {
		record["truncated"] = truncated
	}
	parts := make([][]byte, 0, len(chunks))
	encodedBytes := 0
	for index, chunk := range chunks {
		chunk.object[chunk.key] = chunk.text
		record["fragment_index"] = index + 1
		encoded, err := json.Marshal(record)
		chunk.object[chunk.key] = ""
		if err != nil {
			return nil, fmt.Errorf("encode log fragment: %w", err)
		}
		encodedBytes += len(encoded) + 1
		if len(encoded)+1 > MaxExportRecordBytes || encodedBytes > maxFragmentRecordBytes {
			return nil, errLogRecordTooLarge
		}
		parts = append(parts, append(encoded, '\n'))
	}
	return parts, nil
}

// fragmentField selects the largest bounded text, including formatted errors
// whose attribute name is chosen by the caller. Reserved metadata stays intact.
func fragmentField(record map[string]any, depth int) (map[string]any, string, string) {
	if depth > diagnosticMaxDepth {
		return nil, "", ""
	}
	var selected map[string]any
	var key, text string
	for name, value := range record {
		if candidate, ok := value.(string); ok && fragmentableText(name, candidate) && len(candidate) > len(text) {
			selected, key, text = record, name, candidate
		}
		if child, ok := value.(map[string]any); ok {
			object, childKey, childText := fragmentField(child, depth+1)
			if len(childText) > len(text) {
				selected, key, text = object, childKey, childText
			}
		}
	}
	return selected, key, text
}

func fragmentableText(name, text string) bool {
	if name == "msg" || name == "cause" {
		return true
	}
	switch name {
	case "time", "level", "component", "event", "stream", "operation", "operation_id",
		"record_id", "fragment_index", "fragment_count", "truncated":
		return false
	}
	// cleanAttr already bounds arbitrary error attributes. Larger unrelated
	// metadata must be reported as a capacity failure, not silently shortened.
	return len(text) <= diagnosticMaxBytes
}
