package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type sessionDiagnosticBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *sessionDiagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *sessionDiagnosticBuffer) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.String()
}
func sessionJSONReporter(out io.Writer) diagnostics.Reporter {
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	redactor := logging.NewRedactor("session-private-token")
	return logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(out, level, "tui", redactor)), redactor)
}
func sessionRecords(t *testing.T, out *sessionDiagnosticBuffer) []map[string]any {
	t.Helper()
	raw := out.text()
	if strings.Contains(raw, "session-private-token") || strings.Contains(raw, "example.invalid") {
		t.Fatal("private failure data leaked")
	}
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
		for _, key := range []string{"component", "operation_id", "operation", "cause"} {
			if strings.Count(line, `"`+key+`":`) != 1 {
				t.Fatalf("duplicate/missing %s", key)
			}
		}
		if record["component"] != "tui.session" || record["operation_id"] != "session-local" || record["operation"] != "stream.observe" {
			t.Fatalf("record=%v", record)
		}
	}
	return records
}
func sessionContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return logging.WithOperation(ctx, logging.OperationMetadata{ID: "session-local", Name: "stream.observe"})
}

type failureSessionClient struct {
	*fakeClient
	err    error
	decode bool
}

func (c failureSessionClient) Status(context.Context) (protocol.Status, error) {
	return protocol.Status{}, protocol.APIError{Code: protocol.CodePermissionDenied, Message: "fresh status category"}
}
func (c failureSessionClient) Stream(ctx context.Context, kind string, receive func(protocol.StreamEvent) error) error {
	if kind != "logs" {
		<-ctx.Done()
		return ctx.Err()
	}
	if c.decode {
		return receive(protocol.StreamEvent{Schema: "mihari/v1", Stream: "logs", Data: []byte(`{"secret":"session-private-token"`)})
	}
	return c.err
}
func TestSessionDiagnostics_FailureOwnershipAndFreshStatus(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		decode       bool
		count        int
		event, level string
	}{
		{name: "unreported", err: errors.New("https://example.invalid/?token=session-private-token"), count: 1, event: "streams_failed", level: "ERROR"},
		{name: "reported", err: diagnostics.MarkReported(protocol.APIError{Code: protocol.CodeDataFailure, Message: "already recorded"}), count: 0},
		{name: "callback decode", decode: true, count: 1, event: "streams_failed", level: "ERROR"},
		{name: "normal end", count: 1, event: "streams_ended", level: "DEBUG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := new(sessionDiagnosticBuffer)
			s := New(failureSessionClient{newFakeClient(), tc.err, tc.decode}, Options{Reporter: sessionJSONReporter(out), PollInterval: time.Hour})
			err := s.superviseStreams(sessionContext(t))
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodePermissionDenied {
				t.Fatalf("fresh status replaced: %v", err)
			}
			records := sessionRecords(t, out)
			if len(records) != tc.count {
				t.Fatalf("diagnostics=%d want %d", len(records), tc.count)
			}
			if tc.count != 0 && (records[0]["msg"] != tc.event || records[0]["level"] != tc.level) {
				t.Fatalf("record=%v", records[0])
			}
			if tc.decode && !strings.Contains(records[0]["cause"].(string), "configuration parse error") {
				t.Fatal("decode cause missing")
			}
		})
	}
}

type gatedSessionClient struct {
	*fakeClient
	stopped chan string
	release chan struct{}
}

func (c *gatedSessionClient) Stream(ctx context.Context, kind string, _ func(protocol.StreamEvent) error) error {
	c.started <- kind
	<-ctx.Done()
	c.stopped <- kind
	<-c.release
	return ctx.Err()
}
func TestSessionDiagnostics_TerminalPathsJoinActiveProducers(t *testing.T) {
	for _, mode := range []string{"close", "status failure"} {
		t.Run(mode, func(t *testing.T) {
			fake := &gatedSessionClient{newFakeClient(), make(chan string, 4), make(chan struct{})}
			var release sync.Once
			unblock := func() { release.Do(func() { close(fake.release) }) }
			defer unblock()
			out := new(sessionDiagnosticBuffer)
			s := New(fake, Options{Reporter: sessionJSONReporter(out), PollInterval: time.Millisecond})
			ctx, cancel := context.WithCancel(sessionContext(t))
			defer cancel()
			events := s.Start(ctx)
			defer s.Close()
			// Cleanup must release the gated transport before Close can join it.
			defer unblock()
			waitForEvent(t, events, EventConnected)
			waitForStreamStarts(t, fake.started, 4)
			done := make(chan struct{})
			if mode == "close" {
				go func() { s.Close(); close(done) }()
			} else {
				fake.failNextStatus()
			}
			waitForStreamStarts(t, fake.stopped, 4)
			if mode == "close" {
				select {
				case <-done:
					t.Fatal("Close returned before old producers joined")
				case <-time.After(30 * time.Millisecond):
				}
			}
			if mode == "status failure" {
				timer := time.NewTimer(30 * time.Millisecond)
				defer timer.Stop()
				waiting := true
				for waiting {
					select {
					case event := <-events:
						if event.Kind == EventReconnecting {
							t.Fatal("Status reconnect published before old producers joined")
						}
					case <-timer.C:
						waiting = false
					}
				}
			}
			unblock()
			if mode == "close" {
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("Close did not finish after producer release")
				}
			} else {
				waitForEvent(t, events, EventReconnecting)
			}
			cancel()
			s.Close()
			if len(sessionRecords(t, out)) != 0 {
				t.Fatal("normal producer cancellation diagnosed")
			}
		})
	}
}

type controlledSessionClient struct {
	*fakeClient
	commands  chan bool
	delivered chan struct{}
	active    int
	overlap   bool
}

func (c *controlledSessionClient) Stream(ctx context.Context, kind string, receive func(protocol.StreamEvent) error) error {
	c.mu.Lock()
	c.active++
	if c.active > 4 {
		c.overlap = true
	}
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.active--; c.mu.Unlock() }()
	c.started <- kind
	if kind != "traffic" {
		<-ctx.Done()
		return ctx.Err()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case data := <-c.commands:
			if !data {
				return io.ErrUnexpectedEOF
			}
			if err := receive(protocol.StreamEvent{Schema: "mihari/v1", Stream: "traffic", Data: []byte(`{"up":1,"down":2}`)}); err != nil {
				return err
			}
			c.delivered <- struct{}{}
		}
	}
}
func TestSessionDiagnostics_BackoffRecoveryAndJoinedReopen(t *testing.T) {
	fake := &controlledSessionClient{fakeClient: newFakeClient(), commands: make(chan bool), delivered: make(chan struct{}, 8)}
	out := new(sessionDiagnosticBuffer)
	report := sessionJSONReporter(out)
	observed := make(chan string, 16)
	attempts := make(chan int, 4)
	s := New(fake, Options{PollInterval: time.Hour, Backoff: func(n int) time.Duration { attempts <- n; return 0 }, Reporter: func(ctx context.Context, r diagnostics.Record) { report(ctx, r); observed <- r.Event }})
	events := s.Start(sessionContext(t))
	defer s.Close()
	waitForEvent(t, events, EventConnected)
	waitForStreamStarts(t, fake.started, 4)
	for _, want := range []int{1, 2} {
		fake.commands <- false
		if got := waitForAttempt(t, attempts); got != want {
			t.Fatalf("attempt=%d want %d", got, want)
		}
		waitForStreamStarts(t, fake.started, 4)
	}
	fake.commands <- true
	deadline := time.After(3 * time.Second)
	recovered := false
	for !recovered {
		select {
		case event := <-observed:
			recovered = event == "streams_recovered"
		case <-deadline:
			t.Fatal("stream data recovery was not diagnosed")
		}
	}
	for range 3 {
		fake.commands <- true
	}
	for range 4 {
		select {
		case <-fake.delivered:
		case <-time.After(3 * time.Second):
			t.Fatal("traffic delivery blocked")
		}
	}
	fake.commands <- false
	if got := waitForAttempt(t, attempts); got != 1 {
		t.Fatalf("recovery did not reset backoff: %d", got)
	}
	waitForStreamStarts(t, fake.started, 4)
	s.Close()
	fake.mu.Lock()
	active, overlap := fake.active, fake.overlap
	fake.mu.Unlock()
	if active != 0 || overlap {
		t.Fatalf("producer ownership: active=%d overlap=%v", active, overlap)
	}
	records := sessionRecords(t, out)
	if len(records) != 4 {
		t.Fatalf("failure/recovery count=%d want 4", len(records))
	}
	for i, record := range records {
		wantEvent, wantLevel := "streams_failed", "ERROR"
		if i == 2 {
			wantEvent, wantLevel = "streams_recovered", "INFO"
		}
		if record["msg"] != wantEvent || record["level"] != wantLevel {
			t.Fatalf("record %d=%v", i, record)
		}
	}
}

type streamFailureTransport struct{ failOnlyLogs bool }

func (transport streamFailureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/v1/status" {
		if transport.failOnlyLogs {
			return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"schema":"mihari.error/v1","error":{"code":"permission_denied","message":"fresh status failed"}}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"schema":"mihari/v1"}`))}, nil
	}
	if transport.failOnlyLogs && r.URL.Path != "/v1/streams/logs" {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	return nil, io.ErrUnexpectedEOF
}
func TestSessionDiagnostics_CloseJoinsRealClientReports(t *testing.T) {
	out := new(sessionDiagnosticBuffer)
	report := sessionJSONReporter(out)
	entered := make(chan string, 4)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	c := controlclient.NewHTTP("http://mihari", "session-private-token", &http.Client{Transport: streamFailureTransport{}})
	if err := c.SetDiagnosticReporter(func(ctx context.Context, r diagnostics.Record) { entered <- r.Event; <-release; report(ctx, r) }); err != nil {
		t.Fatal(err)
	}
	s := New(c, Options{Reporter: report, PollInterval: time.Hour})
	events := s.Start(sessionContext(t))
	defer s.Close()
	defer unblock()
	waitForEvent(t, events, EventConnected)
	waitForStreamStarts(t, entered, 4)
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("session closed before client diagnostics completed")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not join diagnostic reporters")
	}
	raw := out.text()
	decoder := json.NewDecoder(strings.NewReader(raw))
	count := 0
	for {
		var record map[string]any
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		if record["component"] != "control.client" || record["msg"] != "stream_failed" || record["level"] != "ERROR" || record["operation_id"] != "session-local" {
			t.Fatalf("duplicate or wrong diagnostic: %v", record)
		}
	}
	if count != 4 {
		t.Fatalf("completed client reports=%d want 4", count)
	}
	if strings.Contains(raw, "session-private-token") {
		t.Fatal("private token leaked")
	}
}

func TestSessionDiagnostics_RealClientFailureIsNotReportedTwice(t *testing.T) {
	out := new(sessionDiagnosticBuffer)
	report := sessionJSONReporter(out)
	c := controlclient.NewHTTP("http://mihari", "session-private-token", &http.Client{Transport: streamFailureTransport{failOnlyLogs: true}})
	if err := c.SetDiagnosticReporter(report); err != nil {
		t.Fatal(err)
	}
	s := New(c, Options{Reporter: report, PollInterval: time.Hour})
	err := s.superviseStreams(sessionContext(t))
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodePermissionDenied || api.Message != "fresh status failed" {
		t.Fatalf("fresh Status error changed: %v", err)
	}
	decoder := json.NewDecoder(strings.NewReader(out.text()))
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record["component"] != "control.client" || record["msg"] != "stream_failed" || record["level"] != "ERROR" || record["operation_id"] != "session-local" {
		t.Fatalf("record=%v", record)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("session duplicated client diagnostic")
	}
}
