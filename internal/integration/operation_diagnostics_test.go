package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

const (
	diagnosticsIPCToken       = "control-token-for-diagnostics"
	diagnosticsIPCSecret      = "controller-secret-for-diagnostics"
	diagnosticsIPCURL         = "https://user:subscription-token@secret.example/sub?token=subscription-token"
	diagnosticsIPCConfig      = "proxies:\n  - name: controller-secret-for-diagnostics"
	diagnosticsIPCConfigStart = "proxies:"
	diagnosticsIPCConfigLine  = "  - name:"
	diagnosticsIPCFailureID   = "settings-ipc"
	diagnosticsIPCNextFailure = "settings-ipc-next"
	diagnosticsIPCWarningID   = "settings-ipc-warning"
)

func TestOperationDiagnosticsIPC_SaveFailureReplayAndNewExecution(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: diagnosticsIPCURL, Err: os.ErrPermission}
	fixture := newOperationDiagnosticsIPCFixture(t, config.CommitResult{}, cause)

	request := protocol.LoggingUpdateRequest{OperationID: diagnosticsIPCFailureID, Level: stringPointer("debug")}
	_, err := fixture.client.UpdateLogging(context.Background(), request)
	assertIPCDataFailure(t, err)
	rawFailure := fixture.responses.At(t, 0)
	assertRawErrorEnvelope(t, rawFailure)
	assertNoDiagnosticSecrets(t, fixture.daemonLogs.String(), fixture.clientLogs.String(), string(rawFailure), err.Error())
	assertDiagnosticLogs(t, fixture.daemonLogs.String(), slog.LevelError, diagnosticsIPCFailureID, 1)
	assertDiagnosticDetail(t, fixture.daemonLogs.String(), slog.LevelError, diagnosticsIPCFailureID, "runtime", "operation.failed", "permission denied")
	assertClientOperationLogs(t, fixture.clientLogs.String(), diagnosticsIPCFailureID, "logging_update_response")
	if got := fixture.saver.CallCount(); got != 1 {
		t.Fatalf("save calls after first request=%d want 1", got)
	}

	_, replayErr := fixture.client.UpdateLogging(context.Background(), request)
	assertIPCDataFailure(t, replayErr)
	rawReplay := fixture.responses.At(t, 1)
	assertRawErrorEnvelope(t, rawReplay)
	assertNoDiagnosticSecrets(t, string(rawReplay), replayErr.Error())
	if got := fixture.saver.CallCount(); got != 1 {
		t.Fatalf("save calls after replay=%d want 1", got)
	}
	assertDiagnosticLogs(t, fixture.daemonLogs.String(), slog.LevelError, diagnosticsIPCFailureID, 1)

	_, nextErr := fixture.client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{
		OperationID: diagnosticsIPCNextFailure,
		Level:       stringPointer("debug"),
	})
	assertIPCDataFailure(t, nextErr)
	rawNextFailure := fixture.responses.At(t, 2)
	assertRawErrorEnvelope(t, rawNextFailure)
	assertNoDiagnosticSecrets(t, string(rawNextFailure), nextErr.Error())
	if got := fixture.saver.CallCount(); got != 2 {
		t.Fatalf("save calls after new operation=%d want 2", got)
	}
	assertDiagnosticLogs(t, fixture.daemonLogs.String(), slog.LevelError, diagnosticsIPCNextFailure, 1)
	assertDiagnosticDetail(t, fixture.daemonLogs.String(), slog.LevelError, diagnosticsIPCNextFailure, "runtime", "operation.failed", "permission denied")
	assertNoDuplicateTopLevelJSONKeys(t, fixture.daemonLogs.String())
	assertNoDuplicateTopLevelJSONKeys(t, fixture.clientLogs.String())
}

func TestOperationDiagnosticsIPC_CommittedWarningPreservesSuccess(t *testing.T) {
	warning := &os.PathError{Op: "sync", Path: diagnosticsIPCConfig, Err: os.ErrPermission}
	fixture := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true, Warning: warning}, nil)

	status, err := fixture.client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{
		OperationID: diagnosticsIPCWarningID,
		Level:       stringPointer("warn"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Schema != "mihari/v1" || status.Revision != 1 || status.Level != "warn" {
		t.Fatalf("status=%#v", status)
	}
	rawSuccess := fixture.responses.At(t, 0)
	assertRawLoggingStatus(t, rawSuccess, status)
	assertNoDiagnosticSecrets(t, string(rawSuccess))
	got, err := fixture.manager.LoggingStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Level != "warn" || got.Revision != 1 {
		t.Fatalf("manager logging status=%#v", got)
	}
	if got := fixture.saver.CallCount(); got != 1 {
		t.Fatalf("save calls=%d want 1", got)
	}
	assertDiagnosticLogs(t, fixture.daemonLogs.String(), slog.LevelWarn, diagnosticsIPCWarningID, 1)
	assertDiagnosticDetail(t, fixture.daemonLogs.String(), slog.LevelWarn, diagnosticsIPCWarningID, "settings", "persist.warning", "permission denied")
	if strings.Contains(fixture.daemonLogs.String(), `"level":"ERROR"`) {
		t.Fatalf("committed warning produced ERROR log: %s", fixture.daemonLogs.String())
	}
	assertClientOperationLogs(t, fixture.clientLogs.String(), diagnosticsIPCWarningID, "logging_update_succeeded")
	assertNoDiagnosticSecrets(t, fixture.daemonLogs.String(), fixture.clientLogs.String(), string(rawSuccess))
	assertNoDuplicateTopLevelJSONKeys(t, fixture.daemonLogs.String())
	assertNoDuplicateTopLevelJSONKeys(t, fixture.clientLogs.String())
}

type operationDiagnosticsIPCFixture struct {
	client     *controlclient.Client
	manager    *runtime.Manager
	saver      *ipcSettingsSaver
	daemonLogs *synchronizedBuffer
	clientLogs *synchronizedBuffer
	responses  *wireResponseCapture
	cancel     context.CancelFunc
	done       <-chan error
}

func newOperationDiagnosticsIPCFixture(t *testing.T, result config.CommitResult, saveErr error, configure ...func(*runtime.Options)) *operationDiagnosticsIPCFixture {
	t.Helper()
	redactor := logging.NewRedactor(diagnosticsIPCToken, diagnosticsIPCSecret, "subscription-token")
	daemonLogs := new(synchronizedBuffer)
	clientLogs := new(synchronizedBuffer)
	daemonLevel, clientLevel := new(slog.LevelVar), new(slog.LevelVar)
	daemonLevel.Set(slog.LevelDebug)
	clientLevel.Set(slog.LevelDebug)
	daemonReporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(daemonLogs, daemonLevel, "daemon", redactor)), redactor)
	clientReporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(clientLogs, clientLevel, "client", redactor)), redactor)
	saver := &ipcSettingsSaver{result: result, err: saveErr}
	store := state.NewStore(state.Snapshot{Health: state.HealthOK})
	options := runtime.Options{
		Store:        store,
		Settings:     config.Defaults(),
		SettingsPath: filepath.Join(t.TempDir(), "settings.yaml"),
		Logging:      &ipcLoggingRuntime{cfg: logging.DefaultConfig(), dir: "logs"},
		SaveSettings: saver.Save,
		DiagnosticReporter: func(ctx context.Context, record diagnostics.Record) {
			daemonReporter(ctx, record)
		},
	}
	for _, apply := range configure {
		apply(&options)
	}
	manager := runtime.New(options)
	endpoint := transporttest.Endpoint(t)
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	server := controlserver.New(controlserver.Options{
		Token: diagnosticsIPCToken, Store: store, Runtime: manager, DiagnosticReporter: daemonReporter,
	})
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(ready)
		done <- server.Serve(ctx, listener)
	}()
	responses := new(wireResponseCapture)
	clientTransport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return transport.DialContext(ctx, endpoint)
	}}
	client := controlclient.NewHTTP("http://mihari", diagnosticsIPCToken, &http.Client{
		Transport: responses.Wrap(clientTransport), Timeout: 10 * time.Second,
	})
	fixture := &operationDiagnosticsIPCFixture{
		client: client, manager: manager, saver: saver, daemonLogs: daemonLogs, clientLogs: clientLogs, responses: responses,
		cancel: cancel, done: done,
	}
	t.Cleanup(func() { fixture.close(t) })
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("daemon stopped before ready: %v", err)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("daemon did not become ready")
	}
	if err := client.SetDiagnosticReporter(clientReporter); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *operationDiagnosticsIPCFixture) close(t *testing.T) {
	t.Helper()
	f.cancel()
	select {
	case err := <-f.done:
		if err != nil {
			t.Errorf("diagnostics IPC daemon stopped with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("diagnostics IPC daemon did not stop")
	}
}

type ipcSettingsSaver struct {
	mu     sync.Mutex
	result config.CommitResult
	err    error
	calls  int
}

func (s *ipcSettingsSaver) Save(string, config.Settings) (config.CommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.result, s.err
}

func (s *ipcSettingsSaver) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type ipcLoggingRuntime struct {
	mu  sync.Mutex
	cfg logging.Config
	dir string
}

func (r *ipcLoggingRuntime) Apply(_ context.Context, cfg logging.Config) {
	r.mu.Lock()
	r.cfg = cfg
	r.mu.Unlock()
}

func (r *ipcLoggingRuntime) Config() logging.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

func (r *ipcLoggingRuntime) Dir() string { return r.dir }

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

const maxCapturedWireResponse = 4 << 20

type wireResponseCapture struct {
	mu        sync.Mutex
	responses [][]byte
	truncated bool
}

func (c *wireResponseCapture) Wrap(next http.RoundTripper) http.RoundTripper {
	return wireResponseCaptureTransport{next: next, capture: c}
}

func (c *wireResponseCapture) add(body []byte, truncated bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses = append(c.responses, append([]byte(nil), body...))
	c.truncated = c.truncated || truncated
}

func (c *wireResponseCapture) At(t *testing.T, index int) []byte {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.truncated {
		t.Fatal("captured wire response exceeded the bounded capture limit")
	}
	if index >= len(c.responses) {
		t.Fatalf("captured responses=%d want index %d", len(c.responses), index)
	}
	return append([]byte(nil), c.responses[index]...)
}

type wireResponseCaptureTransport struct {
	next    http.RoundTripper
	capture *wireResponseCapture
}

func (t wireResponseCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.next.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	response.Body = &wireCaptureBody{ReadCloser: response.Body, capture: t.capture}
	return response, nil
}

type wireCaptureBody struct {
	io.ReadCloser
	capture   *wireResponseCapture
	mu        sync.Mutex
	body      bytes.Buffer
	truncated bool
	closed    bool
}

func (b *wireCaptureBody) Read(value []byte) (int, error) {
	n, err := b.ReadCloser.Read(value)
	if n > 0 {
		b.mu.Lock()
		remaining := maxCapturedWireResponse - b.body.Len()
		if remaining <= 0 {
			b.truncated = true
		} else {
			captured := n
			if captured > remaining {
				b.truncated = true
				captured = remaining
			}
			_, _ = b.body.Write(value[:captured])
		}
		b.mu.Unlock()
	}
	return n, err
}

func (b *wireCaptureBody) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	_, _ = io.Copy(io.Discard, b)
	err := b.ReadCloser.Close()
	b.mu.Lock()
	body := append([]byte(nil), b.body.Bytes()...)
	truncated := b.truncated
	b.mu.Unlock()
	b.capture.add(body, truncated)
	return err
}

func assertIPCDataFailure(t *testing.T, err error) {
	t.Helper()
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "persist settings" || len(apiError.Details) != 0 {
		t.Fatalf("error=%#v", err)
	}
}

func assertDiagnosticLogs(t *testing.T, logs string, level slog.Level, operationID string, want int) {
	t.Helper()
	count := 0
	for _, record := range parseDiagnosticJSONLines(t, logs) {
		if record["level"] == level.String() && record["operation_id"] == operationID {
			count++
		}
	}
	if count != want {
		t.Fatalf("level=%s operation_id=%q records=%d want=%d logs=%s", level, operationID, count, want, logs)
	}
}

func assertClientOperationLogs(t *testing.T, logs, operationID, event string) {
	t.Helper()
	found := false
	for _, record := range parseDiagnosticJSONLines(t, logs) {
		if record["operation_id"] != operationID || record["operation"] != "logging.update" {
			t.Fatalf("client operation fields=%v", record)
		}
		if record["msg"] == event {
			found = true
		}
	}
	if !found {
		t.Fatalf("event %q not found in client logs: %s", event, logs)
	}
}

func assertDiagnosticDetail(t *testing.T, logs string, level slog.Level, operationID, component, event, cause string) {
	t.Helper()
	for _, record := range parseDiagnosticJSONLines(t, logs) {
		if record["level"] == level.String() && record["operation_id"] == operationID {
			if record["component"] != component || record["msg"] != event || !strings.Contains(record["cause"], cause) {
				t.Fatalf("diagnostic record=%v want component=%q event=%q cause containing %q", record, component, event, cause)
			}
			return
		}
	}
	t.Fatalf("missing diagnostic record level=%s operation_id=%q", level, operationID)
}

func assertNoDiagnosticSecrets(t *testing.T, values ...string) {
	t.Helper()
	for _, value := range values {
		for _, secret := range []string{diagnosticsIPCToken, diagnosticsIPCSecret, diagnosticsIPCURL, diagnosticsIPCConfig, diagnosticsIPCConfigStart, diagnosticsIPCConfigLine, "subscription-token"} {
			if strings.Contains(value, secret) {
				t.Fatalf("diagnostic output leaked %q: %s", secret, value)
			}
		}
	}
}

func assertNoDuplicateTopLevelJSONKeys(t *testing.T, logs string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		decodeJSONObjectMembers(t, line)
	}
}

func assertRawErrorEnvelope(t *testing.T, raw []byte) {
	t.Helper()
	members := decodeJSONObjectMembers(t, string(raw))
	assertJSONKeySet(t, members, "schema", "error")
	var schema string
	if err := json.Unmarshal(members["schema"], &schema); err != nil || schema != "mihari.error/v1" {
		t.Fatalf("schema=%q err=%v raw=%s", schema, err, raw)
	}
	errorMembers := decodeJSONObjectMembers(t, string(members["error"]))
	assertJSONKeySet(t, errorMembers, "code", "message")
	var apiError protocol.APIError
	if err := json.Unmarshal(members["error"], &apiError); err != nil || apiError.Code != protocol.CodeDataFailure || apiError.Message != "persist settings" || len(apiError.Details) != 0 {
		t.Fatalf("wire error=%#v err=%v raw=%s", apiError, err, raw)
	}
}

func assertRawLoggingStatus(t *testing.T, raw []byte, want protocol.LoggingStatus) {
	t.Helper()
	members := decodeJSONObjectMembers(t, string(raw))
	assertJSONKeySet(t, members, "schema", "revision", "level", "max_size_mb", "max_files", "dir")
	var got protocol.LoggingStatus
	if err := json.Unmarshal(raw, &got); err != nil || got != want {
		t.Fatalf("wire status=%#v want=%#v err=%v raw=%s", got, want, err, raw)
	}
}

func decodeJSONObjectMembers(t *testing.T, document string) map[string]json.RawMessage {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(document))
	token, err := decoder.Token()
	if err != nil {
		t.Fatalf("read JSON start %q: %v", document, err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		t.Fatalf("JSON value is not an object: %q", document)
	}
	members := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("read JSON key %q: %v", document, err)
		}
		key, ok := token.(string)
		if !ok {
			t.Fatalf("JSON key=%T in %q", token, document)
		}
		if _, exists := members[key]; exists {
			t.Fatalf("duplicate JSON key %q in %q", key, document)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("read JSON value for %q in %q: %v", key, document, err)
		}
		members[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		t.Fatalf("read JSON end %q: %v", document, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("trailing JSON content %q: %v", document, err)
	}
	return members
}

func assertJSONKeySet(t *testing.T, members map[string]json.RawMessage, want ...string) {
	t.Helper()
	if len(members) != len(want) {
		t.Fatalf("JSON keys=%v want=%v", jsonKeys(members), want)
	}
	for _, key := range want {
		if _, ok := members[key]; !ok {
			t.Fatalf("JSON keys=%v missing=%q", jsonKeys(members), key)
		}
	}
}

func jsonKeys(members map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	return keys
}

func parseDiagnosticJSONLines(t *testing.T, logs string) []map[string]string {
	t.Helper()
	var records []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		var record map[string]string
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func stringPointer(value string) *string { return &value }

func TestSystemProxyDiagnostic_IPCOwnerDedup(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: diagnosticsIPCURL, Err: os.ErrPermission}
	fixture := newOperationDiagnosticsIPCFixture(t, config.CommitResult{}, nil, func(options *runtime.Options) { options.SysProxy = &sysproxy.FakeBackend{GetErr: cause} })
	for _, id := range []string{"proxy-ipc", "proxy-ipc", "proxy-ipc-next"} {
		_, err := fixture.client.EnableSystemProxy(context.Background(), protocol.SystemProxyMutationRequest{OperationID: id})
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != protocol.CodeUpstreamFailure || api.Message != "read system proxy state" {
			t.Fatalf("error=%v", err)
		}
		assertDiagnosticLogs(t, fixture.daemonLogs.String(), slog.LevelError, id, 1)
		assertDiagnosticDetail(t, fixture.daemonLogs.String(), slog.LevelError, id, "runtime", "operation.failed", "permission denied")
	}
	if strings.Contains(fixture.daemonLogs.String(), `"component":"control"`) {
		t.Fatalf("server duplicated owner: %s", fixture.daemonLogs.String())
	}
	if !strings.Contains(fixture.daemonLogs.String(), `"operation":"system_proxy.enable"`) || !strings.Contains(fixture.clientLogs.String(), `"operation":"system_proxy.enable"`) {
		t.Fatal("operation missing from IPC logs")
	}
	assertNoDiagnosticSecrets(t, fixture.daemonLogs.String(), fixture.clientLogs.String())
	assertNoDuplicateTopLevelJSONKeys(t, fixture.daemonLogs.String())
	assertNoDuplicateTopLevelJSONKeys(t, fixture.clientLogs.String())
}

func TestTunDiagnostic_IPCOwnerDedup(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: diagnosticsIPCURL, Err: os.ErrPermission}
	fixture := newOperationDiagnosticsIPCFixture(t, config.CommitResult{}, cause)
	for _, id := range []string{"tun-ipc", "tun-ipc", "tun-ipc-next"} {
		_, err := fixture.client.EnableTun(context.Background(), protocol.TunMutationRequest{OperationID: id, Force: true})
		assertIPCDataFailure(t, err)
		assertDiagnosticLogs(t, fixture.daemonLogs.String(), slog.LevelError, id, 1)
		assertDiagnosticDetail(t, fixture.daemonLogs.String(), slog.LevelError, id, "runtime", "operation.failed", "permission denied")
	}
	if !strings.Contains(fixture.daemonLogs.String(), `"operation":"tun.enable"`) || !strings.Contains(fixture.clientLogs.String(), `"operation":"tun.enable"`) {
		t.Fatal("TUN operation missing from IPC logs")
	}
	assertNoDiagnosticSecrets(t, fixture.daemonLogs.String(), fixture.clientLogs.String())
	assertNoDuplicateTopLevelJSONKeys(t, fixture.daemonLogs.String())
}

func TestGeoIPDiagnostic_IPCRawFailureKeepsInternalEnvelope(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: diagnosticsIPCURL, Err: os.ErrPermission}
	service := geoip.New(geoip.ServiceOptions{CountryPath: filepath.Join(t.TempDir(), "country.mmdb"), ASNPath: filepath.Join(t.TempDir(), "asn.mmdb")})
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	fixture := newOperationDiagnosticsIPCFixture(t, config.CommitResult{}, nil, func(options *runtime.Options) {
		options.GeoIP = service
		options.PrepareGeoIP = func(context.Context) (runtime.GeoIPCandidate, error) { return nil, cause }
	})
	for _, id := range []string{"geoip-ipc", "geoip-ipc", "geoip-ipc-next"} {
		_, err := fixture.client.UpdateGeoIP(context.Background(), protocol.MutationRequest{OperationID: id})
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != protocol.CodeInternal || api.Message != "prepare GeoIP databases: permission denied while accessing local database files" || len(api.Details) != 0 {
			t.Fatalf("raw domain error changed envelope: %v", err)
		}
		assertDiagnosticLogs(t, fixture.daemonLogs.String(), slog.LevelError, id, 1)
		assertDiagnosticDetail(t, fixture.daemonLogs.String(), slog.LevelError, id, "runtime", "operation.failed", "permission denied")
	}
	if !strings.Contains(fixture.daemonLogs.String(), `"operation":"geoip.update"`) || !strings.Contains(fixture.clientLogs.String(), `"operation":"geoip.update"`) {
		t.Fatal("operation metadata missing")
	}
	assertNoDiagnosticSecrets(t, fixture.daemonLogs.String(), fixture.clientLogs.String())
	assertNoDuplicateTopLevelJSONKeys(t, fixture.daemonLogs.String())
}

type ipcPanelFailure struct {
	runtime.PanelService
	failure error
}

func (p ipcPanelFailure) PrepareInstall(context.Context, string, string) (panel.PreparedMutation, error) {
	return nil, p.failure
}
func TestPanelDiagnostic_IPCOwnerDedup(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: diagnosticsIPCURL, Err: os.ErrPermission}
	failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download panel asset failed"}, cause)
	fixture := newOperationDiagnosticsIPCFixture(t, config.CommitResult{}, nil, func(options *runtime.Options) { options.Panels = ipcPanelFailure{failure: failure} })
	for _, id := range []string{"panel-ipc", "panel-ipc", "panel-ipc-next"} {
		_, err := fixture.client.InstallPanel(context.Background(), "zashboard", protocol.PanelInstallRequest{OperationID: id})
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != protocol.CodeNetworkFailure || api.Message != "download panel asset failed" {
			t.Fatalf("error=%v", err)
		}
		assertDiagnosticLogs(t, fixture.daemonLogs.String(), slog.LevelError, id, 1)
		assertDiagnosticDetail(t, fixture.daemonLogs.String(), slog.LevelError, id, "runtime", "operation.failed", "permission denied")
	}
	if !strings.Contains(fixture.daemonLogs.String(), `"operation":"panel.install"`) || !strings.Contains(fixture.clientLogs.String(), `"operation":"panel.install"`) {
		t.Fatal("panel metadata missing")
	}
	assertNoDiagnosticSecrets(t, fixture.daemonLogs.String(), fixture.clientLogs.String())
	assertNoDuplicateTopLevelJSONKeys(t, fixture.daemonLogs.String())
}

type ipcProviderTransport func(*http.Request) (*http.Response, error)

func (f ipcProviderTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestProviderDiagnostic_IPCActualAdapterOwnerDedup(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: diagnosticsIPCURL, Err: os.ErrPermission}
	adapter := mihomo.NewClient("http://127.0.0.1", diagnosticsIPCSecret, &http.Client{Transport: ipcProviderTransport(func(*http.Request) (*http.Response, error) { return nil, cause })})
	fixture := newOperationDiagnosticsIPCFixture(t, config.CommitResult{}, nil, func(options *runtime.Options) { options.Controller = adapter })
	for _, id := range []string{"provider-ipc", "provider-ipc", "provider-ipc-next"} {
		_, err := fixture.client.UpdateRuleProvider(context.Background(), "native", protocol.MutationRequest{OperationID: id})
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != protocol.CodeUpstreamFailure || api.Message != "mihomo controller is unavailable" {
			t.Fatalf("error=%v", err)
		}
		assertDiagnosticLogs(t, fixture.daemonLogs.String(), slog.LevelError, id, 1)
		assertDiagnosticDetail(t, fixture.daemonLogs.String(), slog.LevelError, id, "runtime", "operation.failed", "permission denied")
	}
	if !strings.Contains(fixture.daemonLogs.String(), `"operation":"rule_provider.refresh"`) || !strings.Contains(fixture.clientLogs.String(), `"operation":"rule_provider.refresh"`) {
		t.Fatal("provider metadata missing")
	}
	assertNoDiagnosticSecrets(t, fixture.daemonLogs.String(), fixture.clientLogs.String())
	assertNoDuplicateTopLevelJSONKeys(t, fixture.daemonLogs.String())
}
