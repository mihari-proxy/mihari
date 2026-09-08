package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"github.com/mihari-proxy/mihari/internal/tundetect"
)

const fixtureSnapshotID = "00000000000000000000000000000001"

var fixturePayload = []byte(`{"time":"2026-09-05T00:00:00Z","msg":"节点","n":1e0}`)

func TestLoggingSnapshot_StreamsFixtureFramesAndEmptySourceEnd(t *testing.T) {
	source := newFixtureSnapshotSource()
	server := newSnapshotServer(t, source)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, snapshotRequest(`{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("content-type=%q", got)
	}
	want, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", "machine-snapshot-v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recorder.Body.Bytes(), want) {
		t.Fatalf("wire=%s want=%s", recorder.Body.Bytes(), want)
	}
	payload, err := protocol.DecodeMachineLogPayload(parseSnapshotFrames(t, recorder.Body.Bytes())[1].PayloadB64)
	if err != nil || !bytes.Equal(payload, fixturePayload) {
		t.Fatalf("payload bytes changed: %q err=%v", payload, err)
	}
}

func TestLoggingSnapshot_InvalidRequestRejectedBeforeNDJSON(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	valid := `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`
	tests := map[string]string{
		"duplicate key": `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z","to":"2026-09-05T00:00:00Z"}`,
		"missing to":    `{"schema":"mihari.machine-log-request/v1"}`,
		"from after to": `{"schema":"mihari.machine-log-request/v1","from":"2026-09-05T00:00:01Z","to":"2026-09-05T00:00:00Z"}`,
		"to past skew":  `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:05:00.000000001Z"}`,
		"over 4KiB":     valid + strings.Repeat(" ", protocol.MaxMachineLogRequestBytes-len(valid)+1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			server := newSnapshotServer(t, newFixtureSnapshotSource())
			server.now = func() time.Time { return now }
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, snapshotRequest(body))
			assertSnapshotPrestreamError(t, recorder, http.StatusBadRequest, protocol.CodeInvalidArgument)
		})
	}
}

func TestLoggingSnapshot_UnavailableSourceIsInvalidState(t *testing.T) {
	server := newSnapshotServer(t, nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, snapshotRequest(`{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`))
	assertSnapshotPrestreamError(t, recorder, http.StatusConflict, protocol.CodeInvalidState)
}

func TestLoggingSnapshot_ConcurrentAndRateLimits(t *testing.T) {
	t.Run("third concurrent", func(t *testing.T) {
		var opened atomic.Int32
		release := make(chan struct{})
		source := &fakeMachineSnapshotSource{
			onOpen: func(ctx context.Context) error {
				opened.Add(1)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}
		server := newSnapshotServer(t, source)
		done := make(chan struct{}, 2)
		for range 2 {
			go func() {
				defer func() { done <- struct{}{} }()
				recorder := httptest.NewRecorder()
				server.Handler().ServeHTTP(recorder, snapshotRequest(`{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`))
			}()
		}
		waitValue(t, &opened, 2)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, snapshotRequest(`{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`))
		assertSnapshotPrestreamError(t, recorder, http.StatusConflict, protocol.CodeInvalidState)
		close(release)
		waitN(t, done, 2)
	})

	t.Run("seventh admit", func(t *testing.T) {
		server := newSnapshotServer(t, newFixtureSnapshotSource())
		body := `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`
		for i := range 6 {
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, snapshotRequest(body))
			if recorder.Code != http.StatusOK {
				t.Fatalf("admit %d status=%d body=%s", i+1, recorder.Code, recorder.Body.String())
			}
		}
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, snapshotRequest(body))
		assertSnapshotPrestreamError(t, recorder, http.StatusConflict, protocol.CodeInvalidState)
	})
}

func TestLoggingSnapshot_FailureBeforeAndAfterStream(t *testing.T) {
	t.Run("before headers", func(t *testing.T) {
		source := &fakeMachineSnapshotSource{openErr: protocol.APIError{Code: protocol.CodeDataFailure, Message: "truncated log"}}
		server := newSnapshotServer(t, source)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, snapshotRequest(`{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`))
		assertSnapshotPrestreamError(t, recorder, http.StatusInternalServerError, protocol.CodeDataFailure)
	})

	t.Run("after first frame", func(t *testing.T) {
		source := newFixtureSnapshotSource()
		source.daemon.nextErr = errors.New("read failed")
		server := newSnapshotServer(t, source)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, snapshotRequest(`{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Content-Type"); got != "application/x-ndjson" {
			t.Fatalf("content-type=%q", got)
		}
		frames := parseSnapshotFrames(t, recorder.Body.Bytes())
		if len(frames) < 2 || frames[0].Type != "header" || frames[len(frames)-1].Type != "error" {
			t.Fatalf("frames=%+v", frames)
		}
		for _, frame := range frames {
			if frame.Type == "complete" {
				t.Fatal("complete written after stream failure")
			}
		}
		if frames[len(frames)-1].Error.Code != protocol.CodeDataFailure {
			t.Fatalf("error=%#v", frames[len(frames)-1].Error)
		}
	})
}

func TestLoggingSnapshot_CancelStopsWorkers(t *testing.T) {
	entered := make(chan struct{})
	var enterOnce sync.Once
	var nextCanceled atomic.Bool
	source := newFixtureSnapshotSource()
	source.daemon.onNext = func(ctx context.Context) error {
		enterOnce.Do(func() { close(entered) })
		<-ctx.Done()
		nextCanceled.Store(true)
		return ctx.Err()
	}
	server := newSnapshotServer(t, source)
	ctx, cancel := context.WithCancel(context.Background())
	writer := &blockingWriter{ResponseRecorder: httptest.NewRecorder(), ctx: ctx, blockAfter: 1, started: make(chan struct{})}
	started := writer.started
	req := snapshotRequest(`{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Handler().ServeHTTP(writer, req)
	}()
	select {
	case <-started:
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler leaked after cancel")
	}
	t.Cleanup(func() {
		if !nextCanceled.Load() {
			t.Fatal("source next was not cancelled")
		}
	})
}

func TestLoggingSnapshot_ServerShutdownCancelsInFlight(t *testing.T) {
	entered := make(chan struct{})
	var enterOnce sync.Once
	source := newFixtureSnapshotSource()
	source.daemon.onNext = func(ctx context.Context) error {
		enterOnce.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	}
	server := newSnapshotServer(t, source)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx, listener) }()

	req, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/v1/logging/snapshot", strings.NewReader(`{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token")
	clientDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		if resp != nil {
			_, copyErr := io.Copy(io.Discard, resp.Body)
			err = errors.Join(err, copyErr, resp.Body.Close())
		}
		clientDone <- err
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight snapshot never opened the source")
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve leaked in-flight snapshot")
	}
	select {
	case <-clientDone:
	case <-time.After(2 * time.Second):
		t.Fatal("client leaked after shutdown")
	}
}

func TestLoggingSnapshot_CapabilityNotAdvertisedByDefaultManager(t *testing.T) {
	manager := runtimeapi.New(runtimeapi.Options{
		SysProxy:          &sysproxy.FakeBackend{},
		TunDetect:         &tundetect.FakeBackend{},
		LookupTCPOccupant: func(string) (int, bool) { return 0, false },
	})
	if slices.Contains(manager.Capabilities(), protocol.MachineLogSnapshotCapability) {
		t.Fatalf("default capabilities advertised machine snapshot: %v", manager.Capabilities())
	}
}

func TestLoggingSnapshot_ExistingLoggingRoutesStillWork(t *testing.T) {
	status := protocol.LoggingStatus{Schema: "mihari/v1", Revision: 8, Level: "debug", MaxSizeMB: 20, MaxFiles: 5, Dir: "C:/logs"}
	runtime := &loggingTestRuntime{fakeRuntime: &fakeRuntime{}, loggingStatus: status, updateStatus: status}
	server := New(Options{
		Token:          "token",
		Store:          state.NewStore(state.Snapshot{}),
		Runtime:        runtime,
		SnapshotSource: newFixtureSnapshotSource(),
		Now:            func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) },
	})
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, authorizedRequest(http.MethodGet, "/v1/logging", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}
	patch := httptest.NewRecorder()
	server.Handler().ServeHTTP(patch, authorizedRequest(http.MethodPatch, "/v1/logging", bytes.NewBufferString(`{"operation_id":"logging-1","level":"debug"}`)))
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patch.Code, patch.Body.String())
	}
}

func newSnapshotServer(t *testing.T, source logging.MachineSnapshotSource) *Server {
	t.Helper()
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	return New(Options{
		Token:          "token",
		Store:          state.NewStore(state.Snapshot{}),
		Runtime:        &loggingTestRuntime{fakeRuntime: &fakeRuntime{}},
		Now:            func() time.Time { return now },
		SnapshotSource: source,
		SnapshotID:     func() string { return fixtureSnapshotID },
	})
}

func snapshotRequest(body string) *http.Request {
	return authorizedRequest(http.MethodPost, "/v1/logging/snapshot", strings.NewReader(body))
}

func assertSnapshotPrestreamError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode protocol.ErrorCode) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	if ct := recorder.Header().Get("Content-Type"); strings.Contains(ct, "ndjson") {
		t.Fatalf("pre-stream error used ndjson: %s", ct)
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != wantCode {
		t.Fatalf("code=%q want=%q body=%s", envelope.Error.Code, wantCode, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), `"type":"complete"`) || strings.Contains(recorder.Body.String(), "machine-log-stream") {
		t.Fatalf("stream frames written before error: %s", recorder.Body.String())
	}
}

type snapshotTestFrame struct {
	Type       string            `json:"type"`
	PayloadB64 string            `json:"payload_b64"`
	Error      protocol.APIError `json:"error"`
}

func parseSnapshotFrames(t *testing.T, body []byte) []snapshotTestFrame {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	var frames []snapshotTestFrame
	for {
		var frame snapshotTestFrame
		if err := decoder.Decode(&frame); err != nil {
			if errors.Is(err, io.EOF) {
				return frames
			}
			t.Fatalf("decode frame: %v body=%s", err, body)
		}
		frames = append(frames, frame)
	}
}

func waitValue(t *testing.T, got *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("value=%d want=%d", got.Load(), want)
}

func waitN(t *testing.T, done <-chan struct{}, n int) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for range n {
		select {
		case <-done:
		case <-timeout:
			t.Fatal("blocked snapshot did not finish")
		}
	}
}

func newFixtureSnapshotSource() *fakeMachineSnapshotSource {
	return &fakeMachineSnapshotSource{
		daemon: &fakeSourceReader{
			id:      logging.DaemonSource,
			records: []fakeSnapshotRecord{{payload: append([]byte(nil), fixturePayload...)}},
			stats: logging.SourceStats{
				Source: logging.DaemonSource,
				Lines:  1,
				Files:  []string{"mihari-daemon.log"},
				SHA256: "1625f1821f85ab2dc68c7da55c4fbe769637b7752174c7d5be8a83cd8d388a48",
				Bytes:  55,
			},
		},
		mihomo: &fakeSourceReader{
			id: logging.MihomoSource,
			stats: logging.SourceStats{
				Source: logging.MihomoSource,
				Files:  []string{},
				SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
		},
	}
}

type fakeSnapshotRecord struct {
	payload  []byte
	redacted bool
}

type fakeMachineSnapshotSource struct {
	openErr error
	onOpen  func(context.Context) error
	daemon  *fakeSourceReader
	mihomo  *fakeSourceReader
}

func (s *fakeMachineSnapshotSource) Open(ctx context.Context, _ logging.SnapshotWindow) (logging.SnapshotSet, error) {
	if s.onOpen != nil {
		if err := s.onOpen(ctx); err != nil {
			return nil, err
		}
	}
	if s.openErr != nil {
		return nil, s.openErr
	}
	daemon := s.daemon
	if daemon == nil {
		daemon = &fakeSourceReader{id: logging.DaemonSource, stats: logging.SourceStats{Source: logging.DaemonSource, Files: []string{}}}
	}
	mihomo := s.mihomo
	if mihomo == nil {
		mihomo = &fakeSourceReader{id: logging.MihomoSource, stats: logging.SourceStats{Source: logging.MihomoSource, Files: []string{}}}
	}
	return &fakeSnapshotSet{daemon: daemon.clone(), mihomo: mihomo.clone()}, nil
}

type fakeSnapshotSet struct {
	daemon *fakeSourceReader
	mihomo *fakeSourceReader
	next   int
	closed atomic.Bool
}

func (s *fakeSnapshotSet) Source(id logging.SourceID) (logging.SourceReader, error) {
	switch {
	case s.next == 0 && id == logging.DaemonSource:
		s.next = 1
		return s.daemon, nil
	case s.next == 1 && id == logging.MihomoSource:
		s.next = 2
		return s.mihomo, nil
	default:
		return nil, errors.New("invalid machine snapshot source order")
	}
}

func (s *fakeSnapshotSet) Finish(context.Context) error {
	if s.next != 2 || !s.daemon.finished || !s.mihomo.finished {
		return errors.New("machine snapshot is incomplete")
	}
	return nil
}

func (s *fakeSnapshotSet) Close() error {
	s.closed.Store(true)
	return errors.Join(s.daemon.Close(), s.mihomo.Close())
}

type fakeSourceReader struct {
	id       logging.SourceID
	records  []fakeSnapshotRecord
	stats    logging.SourceStats
	onNext   func(context.Context) error
	nextErr  error
	index    int
	eof      bool
	finished bool
	closed   atomic.Bool
}

func (r *fakeSourceReader) clone() *fakeSourceReader {
	if r == nil {
		return &fakeSourceReader{}
	}
	return &fakeSourceReader{
		id:      r.id,
		records: append([]fakeSnapshotRecord(nil), r.records...),
		stats: logging.SourceStats{
			Source:         r.stats.Source,
			Lines:          r.stats.Lines,
			SkippedInvalid: r.stats.SkippedInvalid,
			Redacted:       r.stats.Redacted,
			Files:          append([]string(nil), r.stats.Files...),
			SHA256:         r.stats.SHA256,
			Bytes:          r.stats.Bytes,
		},
		onNext:  r.onNext,
		nextErr: r.nextErr,
	}
}

func (r *fakeSourceReader) Next(ctx context.Context) ([]byte, bool, error) {
	if r.onNext != nil {
		if err := r.onNext(ctx); err != nil {
			return nil, false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if r.nextErr != nil {
		return nil, false, r.nextErr
	}
	if r.index >= len(r.records) {
		r.eof = true
		return nil, false, io.EOF
	}
	record := r.records[r.index]
	r.index++
	return append([]byte(nil), record.payload...), record.redacted, nil
}

func (r *fakeSourceReader) Finish(ctx context.Context) (logging.SourceStats, error) {
	if err := ctx.Err(); err != nil {
		return logging.SourceStats{}, err
	}
	if !r.eof {
		return logging.SourceStats{}, errors.New("machine snapshot source is incomplete")
	}
	r.finished = true
	stats := r.stats
	stats.Files = append([]string(nil), stats.Files...)
	if stats.Files == nil {
		stats.Files = []string{}
	}
	return stats, nil
}

func (r *fakeSourceReader) Close() error {
	r.closed.Store(true)
	return nil
}

type blockingWriter struct {
	*httptest.ResponseRecorder
	ctx        context.Context
	blockAfter int
	started    chan struct{}
	mu         sync.Mutex
	writes     int
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	n := w.writes
	started := w.started
	w.started = nil
	w.mu.Unlock()
	if started != nil {
		close(started)
	}
	if w.blockAfter > 0 && n > w.blockAfter {
		<-w.ctx.Done()
		return 0, w.ctx.Err()
	}
	return w.ResponseRecorder.Write(p)
}
