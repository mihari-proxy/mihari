package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

var (
	snapshotIdleTimeout  = 10 * time.Second
	snapshotTotalTimeout = 125 * time.Second
	snapshotEOFTimeout   = 5 * time.Second
)

type frameResult struct {
	raw []byte
	err error
}

type idleBody struct {
	io.ReadCloser
	mu    sync.Mutex
	idle  time.Duration
	timer *time.Timer
	err   error
}

func newIdleBody(body io.ReadCloser, idle time.Duration) *idleBody {
	b := &idleBody{ReadCloser: body, idle: idle}
	// A zero/short deadline can run the callback before AfterFunc returns.
	// Publish the timer while holding the callback mutex.
	b.mu.Lock()
	b.timer = time.AfterFunc(idle, b.timeout)
	b.mu.Unlock()
	return b
}

func (b *idleBody) timeout() {
	b.mu.Lock()
	if b.timer == nil {
		b.mu.Unlock()
		return
	}
	b.err = snapshotDataFailure()
	b.mu.Unlock()
	_ = b.ReadCloser.Close()
}

func (b *idleBody) resetLocked() {
	if b.timer != nil {
		b.timer.Reset(b.idle)
	}
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	timeoutErr := b.err
	if timeoutErr == nil {
		b.resetLocked()
	}
	b.mu.Unlock()
	if timeoutErr != nil {
		return n, timeoutErr
	}
	return n, err
}

func (b *idleBody) setIdle(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.idle = d
	b.resetLocked()
}

func (b *idleBody) Close() error {
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()
	return b.ReadCloser.Close()
}

type networkSnapshotSet struct {
	ctx      context.Context
	cancel   context.CancelFunc
	body     *idleBody
	frames   <-chan frameResult
	header   protocol.MachineLogHeader
	redactor *logging.Redactor

	mu        sync.Mutex
	next      int
	readers   [2]*networkSourceReader
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

type networkSourceReader struct {
	set            *networkSnapshotSet
	id             logging.SourceID
	redactor       *logging.Redactor
	digest         hash.Hash
	lines          int64
	serverRedacted int64
	orRedacted     int64
	bytes          int64
	end            *protocol.MachineLogSourceEnd
	stats          logging.SourceStats
	eof            bool
	finished       bool
	finishErr      error
	readErr        error
	closed         atomic.Bool
}

// OpenMachineSnapshot starts a machine-log NDJSON stream and returns a SnapshotSet
// that borrows the HTTP response body until Close.
func (c *Client) OpenMachineSnapshot(ctx context.Context, window logging.SnapshotWindow) (logging.SnapshotSet, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if window.To.IsZero() {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid machine snapshot request"}
	}
	body, err := json.Marshal(protocol.MachineLogRequest{
		Schema: protocol.MachineLogRequestSchema,
		From:   window.From,
		To:     window.To.UTC(),
	})
	if err != nil || len(body) > protocol.MaxMachineLogRequestBytes {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid machine snapshot request"}
	}

	totalCtx, totalCancel := context.WithTimeout(ctx, snapshotTotalTimeout)
	request, err := http.NewRequestWithContext(totalCtx, http.MethodPost, c.baseURL+"/v1/logging/snapshot", bytes.NewReader(body))
	if err != nil {
		totalCancel()
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "create control request"}
	}
	token, err := c.requestToken(totalCtx)
	if err != nil {
		totalCancel()
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if c.provider != nil {
		request.GetBody = nil
	}
	response, err := c.snapshotHTTP().Do(request)
	if err != nil {
		totalCancel()
		return nil, snapshotStreamError(c.localError(err), totalCtx, ctx)
	}
	if response.StatusCode != http.StatusOK {
		totalCancel()
		return nil, c.responseError(response)
	}

	idle := newIdleBody(response.Body, snapshotIdleTimeout)
	context.AfterFunc(totalCtx, func() { _ = idle.Close() })
	set := &networkSnapshotSet{
		ctx:      totalCtx,
		cancel:   totalCancel,
		body:     idle,
		redactor: c.redactor,
	}
	set.frames = startFrameReader(totalCtx, idle)
	if err := set.readHeader(); err != nil {
		_ = set.Close()
		return nil, err
	}
	return set, nil
}

func (c *Client) snapshotHTTP() *http.Client {
	base := c.requestHTTP()
	clone := *base
	clone.Timeout = 0
	if tr, ok := clone.Transport.(*http.Transport); ok {
		cloned := tr.Clone()
		cloned.ResponseHeaderTimeout = snapshotIdleTimeout
		clone.Transport = cloned
	}
	return &clone
}

func startFrameReader(ctx context.Context, body io.Reader) <-chan frameResult {
	frames := make(chan frameResult, 1)
	go func() {
		defer close(frames)
		reader := bufio.NewReaderSize(body, 32<<10)
		for {
			raw, err := readSnapshotFrame(reader)
			select {
			case frames <- frameResult{raw: raw, err: err}:
				if err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return frames
}

func readSnapshotFrame(reader *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(buf)+len(fragment) > protocol.MaxMachineLogFrameBytes {
			return nil, snapshotDataFailure()
		}
		buf = append(buf, fragment...)
		if len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
			if len(buf) == 1 {
				return nil, snapshotDataFailure()
			}
			return buf, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(buf) == 0 {
				return nil, io.EOF
			}
			return nil, snapshotDataFailure()
		}
		if err != nil {
			return nil, err
		}
	}
}

func (s *networkSnapshotSet) nextFrame() ([]byte, error) {
	select {
	case fr, ok := <-s.frames:
		if !ok {
			if err := s.ctx.Err(); err != nil {
				return nil, err
			}
			return nil, snapshotDataFailure()
		}
		if fr.err != nil {
			return nil, fr.err
		}
		return fr.raw, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *networkSnapshotSet) readHeader() error {
	raw, err := s.nextFrame()
	if err != nil {
		return snapshotStreamError(err, s.ctx, nil)
	}
	header, err := decodeMachineHeader(raw)
	if err != nil {
		return err
	}
	s.header = header
	return nil
}

func (s *networkSnapshotSet) Source(id logging.SourceID) (logging.SourceReader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return nil, snapshotDataFailure()
	}
	want := logging.DaemonSource
	if s.next == 1 {
		want = logging.MihomoSource
	}
	if s.next >= 2 || id != want || (s.next == 1 && (s.readers[0] == nil || !s.readers[0].finished)) {
		return nil, snapshotDataFailure()
	}
	reader := &networkSourceReader{set: s, id: id, redactor: s.redactor, digest: sha256.New()}
	s.readers[s.next] = reader
	s.next++
	return reader, nil
}

func (s *networkSnapshotSet) Finish(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	r0, r1 := s.readers[0], s.readers[1]
	s.mu.Unlock()
	if r0 == nil || r1 == nil || !r0.finished || !r1.finished {
		return snapshotDataFailure()
	}
	s.body.setIdle(snapshotEOFTimeout)
	eofCtx, cancel := context.WithTimeout(s.ctx, snapshotEOFTimeout)
	defer cancel()

	raw, err := s.nextFrameContext(eofCtx)
	if err != nil {
		return snapshotStreamError(err, eofCtx, ctx)
	}
	complete, err := decodeMachineComplete(raw)
	if err != nil {
		return err
	}
	if complete.SnapshotID != s.header.SnapshotID || complete.SourceCount != 2 || complete.TotalBytes != r0.bytes+r1.bytes {
		return snapshotDataFailure()
	}
	_, err = s.nextFrameContext(eofCtx)
	if !errors.Is(err, io.EOF) {
		return snapshotStreamError(err, eofCtx, ctx)
	}
	return nil
}

func (s *networkSnapshotSet) nextFrameContext(ctx context.Context) ([]byte, error) {
	select {
	case fr, ok := <-s.frames:
		if !ok {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := s.ctx.Err(); err != nil {
				return nil, err
			}
			return nil, snapshotDataFailure()
		}
		if fr.err != nil {
			return nil, fr.err
		}
		return fr.raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *networkSnapshotSet) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.cancel()
		s.closeErr = s.body.Close()
		for range s.frames {
		}
	})
	return s.closeErr
}

func (r *networkSourceReader) Next(ctx context.Context) ([]byte, bool, error) {
	if r.closed.Load() {
		return nil, false, snapshotDataFailure()
	}
	if r.readErr != nil {
		return nil, false, r.readErr
	}
	if r.eof {
		return nil, false, io.EOF
	}
	for {
		if err := ctx.Err(); err != nil {
			r.readErr = err
			return nil, false, err
		}
		raw, err := r.set.nextFrame()
		if err != nil {
			r.readErr = snapshotStreamError(err, r.set.ctx, ctx)
			return nil, false, r.readErr
		}
		meta, fields, err := decodeFrameFields(raw)
		if err != nil {
			r.readErr = err
			return nil, false, err
		}
		switch meta.Type {
		case "record":
			record, err := decodeMachineRecord(fields)
			if err != nil {
				r.readErr = err
				return nil, false, err
			}
			if record.Source != string(r.id) {
				r.readErr = snapshotDataFailure()
				return nil, false, r.readErr
			}
			payload, err := protocol.DecodeMachineLogPayload(record.PayloadB64)
			if err != nil {
				r.readErr = snapshotDataFailure()
				return nil, false, r.readErr
			}
			_, _ = r.digest.Write(payload)
			_, _ = r.digest.Write([]byte{'\n'})
			r.lines++
			if record.Redacted {
				r.serverRedacted++
			}
			r.bytes += int64(len(payload) + 1)
			encoded, changed := secondRedact(r.redactor, payload)
			redacted := record.Redacted || changed
			if redacted {
				r.orRedacted++
			}
			return encoded, redacted, nil
		case "source_end":
			end, err := decodeMachineSourceEnd(fields)
			if err != nil {
				r.readErr = err
				return nil, false, err
			}
			if end.Source != string(r.id) {
				r.readErr = snapshotDataFailure()
				return nil, false, r.readErr
			}
			r.end = &end
			r.eof = true
			return nil, false, io.EOF
		case "error":
			r.readErr = decodeMachineError(fields)
			return nil, false, r.readErr
		default:
			r.readErr = snapshotDataFailure()
			return nil, false, r.readErr
		}
	}
}

func (r *networkSourceReader) Finish(ctx context.Context) (logging.SourceStats, error) {
	if r.finished {
		return r.stats, r.finishErr
	}
	if err := ctx.Err(); err != nil {
		return logging.SourceStats{}, err
	}
	if !r.eof || r.readErr != nil || r.end == nil || (r.closed.Load() && !r.finished) {
		r.finishErr = snapshotDataFailure()
		return logging.SourceStats{}, r.finishErr
	}
	sum := hex.EncodeToString(r.digest.Sum(nil))
	if r.end.Lines != r.lines || r.end.Redacted != r.serverRedacted || r.end.Bytes != r.bytes || r.end.SHA256 != sum {
		r.finishErr = snapshotDataFailure()
		return logging.SourceStats{}, r.finishErr
	}
	files := r.end.Sources
	if files == nil {
		files = []string{}
	}
	r.stats = logging.SourceStats{
		Source:         r.id,
		Lines:          r.lines,
		SkippedInvalid: r.end.SkippedInvalid,
		Redacted:       r.orRedacted,
		Bytes:          r.bytes,
		Files:          append([]string{}, files...),
		SHA256:         sum,
	}
	r.finished = true
	return r.stats, nil
}

func (r *networkSourceReader) Close() error {
	r.closed.Store(true)
	return nil
}

func secondRedact(redactor *logging.Redactor, payload []byte) ([]byte, bool) {
	if redactor == nil {
		redactor = logging.NewRedactor()
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil || record == nil {
		return payload, false
	}
	clean, changed := redactor.Value(record)
	encoded, err := json.Marshal(clean)
	if err != nil {
		return payload, false
	}
	return encoded, changed
}

func decodeFrameFields(raw []byte) (protocol.MachineLogFrameMeta, map[string]json.RawMessage, error) {
	line := bytes.TrimSuffix(raw, []byte{'\n'})
	if len(line) == 0 || line[0] != '{' || bytes.ContainsAny(line, "\r") || !utf8.Valid(line) {
		return protocol.MachineLogFrameMeta{}, nil, snapshotDataFailure()
	}
	fields, err := decodeExactObject(line)
	if err != nil {
		return protocol.MachineLogFrameMeta{}, nil, err
	}
	schema, err := decodeJSONString(fields["schema"])
	if err != nil || schema != protocol.MachineLogStreamSchema {
		return protocol.MachineLogFrameMeta{}, nil, snapshotDataFailure()
	}
	typ, err := decodeJSONString(fields["type"])
	if err != nil {
		return protocol.MachineLogFrameMeta{}, nil, err
	}
	return protocol.MachineLogFrameMeta{Schema: schema, Type: typ}, fields, nil
}

func decodeExactObject(data []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, snapshotDataFailure()
	}
	values := make(map[string]json.RawMessage)
	for dec.More() {
		keyTok, err := dec.Token()
		key, ok := keyTok.(string)
		if err != nil || !ok {
			return nil, snapshotDataFailure()
		}
		if _, exists := values[key]; exists {
			return nil, snapshotDataFailure()
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, snapshotDataFailure()
		}
		values[key] = raw
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return nil, snapshotDataFailure()
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, snapshotDataFailure()
	}
	return values, nil
}

func decodeMachineHeader(raw []byte) (protocol.MachineLogHeader, error) {
	meta, fields, err := decodeFrameFields(raw)
	if err != nil || meta.Type != "header" {
		return protocol.MachineLogHeader{}, snapshotDataFailure()
	}
	if err := requireKeys(fields, "schema", "type", "snapshot_id", "to", "sources"); err != nil {
		return protocol.MachineLogHeader{}, err
	}
	for key := range fields {
		switch key {
		case "schema", "type", "snapshot_id", "from", "to", "sources":
		default:
			return protocol.MachineLogHeader{}, snapshotDataFailure()
		}
	}
	id, err := decodeJSONString(fields["snapshot_id"])
	if err != nil || !isLowerHex(id, 32) {
		return protocol.MachineLogHeader{}, snapshotDataFailure()
	}
	to, err := decodeJSONTime(fields["to"])
	if err != nil {
		return protocol.MachineLogHeader{}, err
	}
	sources, err := decodeJSONStringSlice(fields["sources"])
	if err != nil || len(sources) != 2 || sources[0] != string(logging.DaemonSource) || sources[1] != string(logging.MihomoSource) {
		return protocol.MachineLogHeader{}, snapshotDataFailure()
	}
	header := protocol.MachineLogHeader{
		MachineLogFrameMeta: meta,
		SnapshotID:          id,
		To:                  to,
		Sources:             sources,
	}
	if rawFrom, ok := fields["from"]; ok {
		from, err := decodeJSONTime(rawFrom)
		if err != nil {
			return protocol.MachineLogHeader{}, err
		}
		header.From = &from
	}
	return header, nil
}

func decodeMachineRecord(fields map[string]json.RawMessage) (protocol.MachineLogRecord, error) {
	if err := requireExactKeys(fields, "schema", "type", "source", "payload_b64", "redacted"); err != nil {
		return protocol.MachineLogRecord{}, err
	}
	source, err := decodeJSONString(fields["source"])
	if err != nil {
		return protocol.MachineLogRecord{}, err
	}
	payload, err := decodeJSONString(fields["payload_b64"])
	if err != nil {
		return protocol.MachineLogRecord{}, err
	}
	redacted, err := decodeJSONBool(fields["redacted"])
	if err != nil {
		return protocol.MachineLogRecord{}, err
	}
	return protocol.MachineLogRecord{
		MachineLogFrameMeta: protocol.MachineLogFrameMeta{Schema: protocol.MachineLogStreamSchema, Type: "record"},
		Source:              source,
		PayloadB64:          payload,
		Redacted:            redacted,
	}, nil
}

func decodeMachineSourceEnd(fields map[string]json.RawMessage) (protocol.MachineLogSourceEnd, error) {
	if err := requireExactKeys(fields, "schema", "type", "source", "lines", "skipped_invalid", "redacted", "sources", "sha256", "bytes"); err != nil {
		return protocol.MachineLogSourceEnd{}, err
	}
	source, err := decodeJSONString(fields["source"])
	if err != nil {
		return protocol.MachineLogSourceEnd{}, err
	}
	lines, err := decodeJSONInt64(fields["lines"])
	if err != nil {
		return protocol.MachineLogSourceEnd{}, err
	}
	skipped, err := decodeJSONInt64(fields["skipped_invalid"])
	if err != nil {
		return protocol.MachineLogSourceEnd{}, err
	}
	redacted, err := decodeJSONInt64(fields["redacted"])
	if err != nil {
		return protocol.MachineLogSourceEnd{}, err
	}
	sources, err := decodeJSONStringSlice(fields["sources"])
	if err != nil {
		return protocol.MachineLogSourceEnd{}, err
	}
	sum, err := decodeJSONString(fields["sha256"])
	if err != nil || !isLowerHex(sum, 64) {
		return protocol.MachineLogSourceEnd{}, snapshotDataFailure()
	}
	n, err := decodeJSONInt64(fields["bytes"])
	if err != nil {
		return protocol.MachineLogSourceEnd{}, err
	}
	return protocol.MachineLogSourceEnd{
		MachineLogFrameMeta: protocol.MachineLogFrameMeta{Schema: protocol.MachineLogStreamSchema, Type: "source_end"},
		Source:              source,
		Lines:               lines,
		SkippedInvalid:      skipped,
		Redacted:            redacted,
		Sources:             sources,
		SHA256:              sum,
		Bytes:               n,
	}, nil
}

func decodeMachineComplete(raw []byte) (protocol.MachineLogComplete, error) {
	meta, fields, err := decodeFrameFields(raw)
	if err != nil || meta.Type != "complete" {
		return protocol.MachineLogComplete{}, snapshotDataFailure()
	}
	if err := requireExactKeys(fields, "schema", "type", "snapshot_id", "source_count", "total_bytes"); err != nil {
		return protocol.MachineLogComplete{}, err
	}
	id, err := decodeJSONString(fields["snapshot_id"])
	if err != nil || !isLowerHex(id, 32) {
		return protocol.MachineLogComplete{}, snapshotDataFailure()
	}
	count, err := decodeJSONInt64(fields["source_count"])
	if err != nil {
		return protocol.MachineLogComplete{}, err
	}
	total, err := decodeJSONInt64(fields["total_bytes"])
	if err != nil {
		return protocol.MachineLogComplete{}, err
	}
	return protocol.MachineLogComplete{
		MachineLogFrameMeta: meta,
		SnapshotID:          id,
		SourceCount:         int(count),
		TotalBytes:          total,
	}, nil
}

func decodeMachineError(fields map[string]json.RawMessage) error {
	if err := requireExactKeys(fields, "schema", "type", "error"); err != nil {
		return err
	}
	var api protocol.APIError
	if err := json.Unmarshal(fields["error"], &api); err != nil || api.Code == "" {
		return snapshotDataFailure()
	}
	return api
}

func requireKeys(fields map[string]json.RawMessage, keys ...string) error {
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return snapshotDataFailure()
		}
	}
	return nil
}

func requireExactKeys(fields map[string]json.RawMessage, keys ...string) error {
	if len(fields) != len(keys) {
		return snapshotDataFailure()
	}
	return requireKeys(fields, keys...)
}

func decodeJSONString(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", snapshotDataFailure()
	}
	return value, nil
}

func decodeJSONBool(raw json.RawMessage) (bool, error) {
	if bytes.Equal(raw, []byte("true")) {
		return true, nil
	}
	if bytes.Equal(raw, []byte("false")) {
		return false, nil
	}
	return false, snapshotDataFailure()
}

func decodeJSONInt64(raw json.RawMessage) (int64, error) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, snapshotDataFailure()
	}
	if strings.ContainsAny(string(n), ".eE+") {
		return 0, snapshotDataFailure()
	}
	value, err := n.Int64()
	if err != nil {
		return 0, snapshotDataFailure()
	}
	return value, nil
}

func decodeJSONStringSlice(raw json.RawMessage) ([]string, error) {
	if bytes.Equal(raw, []byte("null")) {
		return nil, snapshotDataFailure()
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, snapshotDataFailure()
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

func decodeJSONTime(raw json.RawMessage) (time.Time, error) {
	text, err := decodeJSONString(raw)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, snapshotDataFailure()
	}
	if _, offset := parsed.Zone(); offset != 0 {
		return time.Time{}, snapshotDataFailure()
	}
	return parsed.UTC(), nil
}

func isLowerHex(value string, n int) bool {
	if len(value) != n {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func snapshotDataFailure() error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "machine snapshot failed"}
}

func snapshotStreamError(err error, streamCtx, callerCtx context.Context) error {
	if err == nil {
		return snapshotDataFailure()
	}
	if callerCtx != nil && errors.Is(callerCtx.Err(), context.Canceled) && errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.Canceled) && streamCtx != nil && errors.Is(streamCtx.Err(), context.Canceled) {
		if callerCtx != nil && callerCtx.Err() == nil {
			return snapshotDataFailure()
		}
		return err
	}
	var api protocol.APIError
	if errors.As(err, &api) {
		return err
	}
	return snapshotDataFailure()
}
