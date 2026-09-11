package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"
	"unicode/utf8"
)

var snapshotUTCPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]00:00)$`)

const (
	// MachineLogSnapshotCapability is advertised only by shared Unix system daemons.
	MachineLogSnapshotCapability = "machine-log-snapshot-v1"
	// MachineLogStreamSchema identifies every NDJSON snapshot frame.
	MachineLogStreamSchema = "mihari.machine-log-stream/v1"
	// MachineLogRequestSchema identifies the bounded snapshot request contract.
	MachineLogRequestSchema = "mihari.machine-log-request/v1"
	// MaxMachineLogRequestBytes includes all request body bytes, including whitespace.
	MaxMachineLogRequestBytes = 4 << 10
	// MaxMachineLogRecordBytes bounds both a physical source record and decoded payload.
	MaxMachineLogRecordBytes = 1 << 20
	// MaxMachineLogFrameBytes includes the frame's terminating LF.
	MaxMachineLogFrameBytes = 2 << 20
)

// MachineLogFrameMeta identifies a versioned frame before its typed body is decoded.
type MachineLogFrameMeta struct {
	Schema string `json:"schema"`
	Type   string `json:"type"`
}

// MachineLogHeader begins a snapshot with its fixed ordered source list.
type MachineLogHeader struct {
	MachineLogFrameMeta
	SnapshotID string     `json:"snapshot_id"`
	From       *time.Time `json:"from,omitempty"`
	To         time.Time  `json:"to"`
	Sources    []string   `json:"sources"`
}

// MachineLogRecord carries one canonical base64-encoded JSON object.
type MachineLogRecord struct {
	MachineLogFrameMeta
	Source     string `json:"source"`
	PayloadB64 string `json:"payload_b64"`
	Redacted   bool   `json:"redacted"`
}

// MachineLogSourceEnd accounts for the exact payload bytes plus one LF per record.
type MachineLogSourceEnd struct {
	MachineLogFrameMeta
	Source         string   `json:"source"`
	Lines          int64    `json:"lines"`
	SkippedInvalid int64    `json:"skipped_invalid"`
	Redacted       int64    `json:"redacted"`
	Sources        []string `json:"sources"`
	SHA256         string   `json:"sha256"`
	Bytes          int64    `json:"bytes"`
}

// MachineLogComplete terminates a snapshot; normal HTTP body EOF must follow.
type MachineLogComplete struct {
	MachineLogFrameMeta
	SnapshotID  string `json:"snapshot_id"`
	SourceCount int    `json:"source_count"`
	TotalBytes  int64  `json:"total_bytes"`
}

// MachineLogFailure terminates a started stream without a complete frame.
type MachineLogFailure struct {
	MachineLogFrameMeta
	Error APIError `json:"error"`
}

// MachineLogRequest selects the same closed UTC window for both machine sources.
// From may be omitted to include all records at or before To.
type MachineLogRequest struct {
	Schema string     `json:"schema"`
	From   *time.Time `json:"from,omitempty"`
	To     time.Time  `json:"to"`
}

// DecodeMachineLogRequest validates the bounded, versioned snapshot request.
func DecodeMachineLogRequest(reader io.Reader, now time.Time) (MachineLogRequest, error) {
	invalid := APIError{Code: CodeInvalidArgument, Message: "invalid machine snapshot request"}
	if reader == nil {
		return MachineLogRequest{}, invalid
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxMachineLogRequestBytes+1))
	if err != nil || len(data) > MaxMachineLogRequestBytes || !utf8.Valid(data) {
		return MachineLogRequest{}, invalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if start, err := d.Token(); err != nil || start != json.Delim('{') {
		return MachineLogRequest{}, invalid
	}
	values := make(map[string]string, 3)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return MachineLogRequest{}, invalid
		}
		name, ok := key.(string)
		if !ok || (name != "schema" && name != "from" && name != "to") {
			return MachineLogRequest{}, invalid
		}
		if _, exists := values[name]; exists {
			return MachineLogRequest{}, invalid
		}
		value, err := d.Token()
		text, ok := value.(string)
		if err != nil || !ok {
			return MachineLogRequest{}, invalid
		}
		values[name] = text
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return MachineLogRequest{}, invalid
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return MachineLogRequest{}, invalid
	}
	if values["schema"] != MachineLogRequestSchema {
		return MachineLogRequest{}, invalid
	}
	to, err := parseSnapshotUTC(values["to"])
	if err != nil || to.After(now.Add(5*time.Minute)) {
		return MachineLogRequest{}, invalid
	}
	request := MachineLogRequest{Schema: MachineLogRequestSchema, To: to}
	if raw, exists := values["from"]; exists {
		from, err := parseSnapshotUTC(raw)
		if err != nil || from.After(to) {
			return MachineLogRequest{}, invalid
		}
		request.From = &from
	}
	return request, nil
}

func parseSnapshotUTC(value string) (time.Time, error) {
	// time.Parse accepts a single-digit hour and a comma fractional separator;
	// neither is part of the declared RFC3339Nano wire grammar.
	if !snapshotUTCPattern.MatchString(value) {
		return time.Time{}, errors.New("invalid snapshot timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	if _, offset := t.Zone(); offset != 0 {
		return time.Time{}, errors.New("snapshot timestamp must be UTC")
	}
	return t.UTC(), nil
}

// DecodeMachineLogPayload preserves the exact JSON bytes used by wire hashes.
func DecodeMachineLogPayload(encoded string) ([]byte, error) {
	invalid := APIError{Code: CodeDataFailure, Message: "invalid machine snapshot payload"}
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxMachineLogRecordBytes) {
		return nil, invalid
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) > MaxMachineLogRecordBytes || !utf8.Valid(payload) || bytes.ContainsAny(payload, "\r\n") {
		return nil, invalid
	}
	// Strict decoding still ignores CR/LF; require the canonical wire spelling.
	if base64.StdEncoding.EncodeToString(payload) != encoded {
		return nil, invalid
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(payload) {
		return nil, invalid
	}
	return payload, nil
}
