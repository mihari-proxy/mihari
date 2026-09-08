package client

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestOpenMachineSnapshot_RejectsMutatedFixtureWithoutPublishing(t *testing.T) {
	fixture := machineSnapshotFixture(t)
	frames := splitSnapshotFrames(t, fixture)
	cases := []struct {
		name string
		body []byte
		hold bool
	}{
		{"truncated", joinSnapshotFrames(frames[:len(frames)-1]), false},
		{"complete-plus-newline", append(append([]byte(nil), fixture...), '\n'), false},
		{"wrong-hash", joinSnapshotFrames(replaceFrameField(frames, "source_end", `"sha256":"1625f1821f85ab2dc68c7da55c4fbe769637b7752174c7d5be8a83cd8d388a48"`, `"sha256":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"`)), false},
		{"wrong-count", joinSnapshotFrames(replaceFrameField(frames, "source_end", `"lines":1`, `"lines":2`)), false},
		{"unexpected-source", joinSnapshotFrames(replaceFrameField(frames, "record", `"source":"daemon"`, `"source":"tui"`)), false},
		{"error-frame", joinSnapshotFrames(append(frames[:len(frames)-1], []byte(`{"schema":"mihari.machine-log-stream/v1","type":"error","error":{"code":"data_failure","message":"machine snapshot failed"}}`))), false},
		{"complete-without-eof", fixture, true},
		{"header-after-record", joinSnapshotFrames([][]byte{frames[1], frames[0], frames[2], frames[3], frames[4]}), false},
		{"source-end-before-records", joinSnapshotFrames([][]byte{frames[0], frames[2], frames[1], frames[3], frames[4]}), false},
		{"duplicate-source-end", joinSnapshotFrames([][]byte{frames[0], frames[1], frames[2], frames[2], frames[3], frames[4]}), false},
		{"duplicate-complete", append(append([]byte(nil), fixture...), append(frames[4], '\n')...), false},
		{"missing-lf", bytes.TrimSuffix(fixture, []byte{'\n'}), false},
		{"base64-padding", joinSnapshotFrames(replaceFrameField(frames, "record", `"payload_b64":"eyJ0aW1lIjoiMjAyNi0wOS0wNVQwMDowMDowMFoiLCJtc2ciOiLoioLngrkiLCJuIjoxZTB9"`, `"payload_b64":"eyJ0aW1lIjoiMjAyNi0wOS0wNVQwMDowMDowMFoiLCJtc2ciOiLoioLngrkiLCJuIjoxZTB9=="`)), false},
		{"base64-whitespace", joinSnapshotFrames(replaceFrameField(frames, "record", `eyJ0aW1lIjoi`, `eyJ0 aW1lIjoi`)), false},
		{"utf8-invalid", joinSnapshotFrames(replaceFrameField(frames, "record", `"payload_b64":"eyJ0aW1lIjoiMjAyNi0wOS0wNVQwMDowMDowMFoiLCJtc2ciOiLoioLngrkiLCJuIjoxZTB9"`, `"payload_b64":"/w=="`)), false},
		{"json-unknown-field", joinSnapshotFrames(replaceFrameField(frames, "header", `}`, `,"path":"secret-value"}`)), false},
		{"json-leading-space", append([]byte{' '}, fixture...), false},
	}
	prevEOF := snapshotEOFTimeout
	snapshotEOFTimeout = 50 * time.Millisecond
	t.Cleanup(func() { snapshotEOFTimeout = prevEOF })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			closed := &atomic.Bool{}
			srv := httptest.NewServer(snapshotBodyHandler(t, tc.body, tc.hold, closed))
			t.Cleanup(srv.Close)
			c := snapshotTestClient(srv)
			req, parent, out := newAssembleRequest(t)
			_, err := assembleMachineSnapshot(t, c, fixtureWindow(), req)
			assertSnapshotDataFailure(t, err)
			assertNoExportResidue(t, parent, out)
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatal("sensitive value leaked")
			}
		})
	}
}

func TestOpenMachineSnapshot_FixturePublishesManifestV2(t *testing.T) {
	fixture := machineSnapshotFixture(t)
	srv := httptest.NewServer(snapshotBodyHandler(t, fixture, false, nil))
	t.Cleanup(srv.Close)
	c := snapshotTestClient(srv)
	req, parent, out := newAssembleRequest(t)
	result, err := assembleMachineSnapshot(t, c, fixtureWindow(), req)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotExportIdentity(t, result.Path, out)
	got := readExportZip(t, out)
	if _, ok := got["manifest.json"]; !ok {
		t.Fatalf("entries=%v", keys(got))
	}
	if _, ok := got["mihomo/mihomo.log"]; ok {
		t.Fatal("empty mihomo entry published")
	}
	if _, ok := got["tui/mihari-tui.log"]; ok {
		t.Fatal("unrequested tui entry published")
	}
	if !strings.Contains(got["daemon/mihari-daemon.log"], `"msg":"节点"`) {
		t.Fatalf("daemon log=%q", got["daemon/mihari-daemon.log"])
	}
	manifest := decodeManifest(t, got["manifest.json"])
	if manifest["schema"] != "mihari-logs-export/v2" {
		t.Fatalf("schema=%v", manifest["schema"])
	}
	if manifest["scope"] != logging.ExportScopeMachineAndCurrentUser {
		t.Fatalf("scope=%v", manifest["scope"])
	}
	status, _ := manifest["source_status"].(map[string]any)
	if status["daemon"] != logging.ExportSourceCollected || status["mihomo"] != logging.ExportSourceCollected || status["tui"] != logging.ExportSourceUnavailable {
		t.Fatalf("source_status=%v", status)
	}
	files, _ := manifest["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files=%v", files)
	}
	file, _ := files[0].(map[string]any)
	if file["name"] != "daemon/mihari-daemon.log" || file["lines"] != json.Number("1") || file["redacted"] != json.Number("0") || file["skipped_invalid"] != json.Number("0") {
		t.Fatalf("file=%v", file)
	}
	sources, _ := file["sources"].([]any)
	if len(sources) != 1 || sources[0] != "mihari-daemon.log" {
		t.Fatalf("sources=%v", sources)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "export.zip" {
		t.Fatalf("parent residue=%v", entries)
	}
}

func TestOpenMachineSnapshot_AllEmptyDoesNotPublish(t *testing.T) {
	empty := joinSnapshotFrames([][]byte{
		[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"header","snapshot_id":"00000000000000000000000000000001","to":"2026-09-05T00:00:00Z","sources":["daemon","mihomo"]}`),
		[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"source_end","source":"daemon","lines":0,"skipped_invalid":0,"redacted":0,"sources":[],"sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","bytes":0}`),
		[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"source_end","source":"mihomo","lines":0,"skipped_invalid":0,"redacted":0,"sources":[],"sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","bytes":0}`),
		[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"complete","snapshot_id":"00000000000000000000000000000001","source_count":2,"total_bytes":0}`),
	})
	srv := httptest.NewServer(snapshotBodyHandler(t, empty, false, nil))
	t.Cleanup(srv.Close)
	c := snapshotTestClient(srv)
	req, parent, out := newAssembleRequest(t)
	_, err := assembleMachineSnapshot(t, c, fixtureWindow(), req)
	if !errors.Is(err, logging.ErrNoLogLines) {
		t.Fatalf("error=%v want ErrNoLogLines", err)
	}
	assertNoExportResidue(t, parent, out)
}

func TestOpenMachineSnapshot_CancelClosesBody(t *testing.T) {
	started := make(chan struct{})
	closed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(machineSnapshotFixture(t)[:len(machineSnapshotFixture(t))/2])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	c := snapshotTestClient(srv)
	req, parent, out := newAssembleRequest(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		set, err := c.OpenMachineSnapshot(ctx, fixtureWindow())
		if err != nil {
			done <- err
			return
		}
		_, err = logging.Assemble(ctx, req, logging.ExportScopeMachineAndCurrentUser, logging.NamedSourcesFromSet(set), set.Finish)
		done <- errors.Join(err, set.Close())
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) && !isSnapshotDataFailure(err) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenMachineSnapshot leaked after cancel")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("response body was not closed")
	}
	assertNoExportResidue(t, parent, out)
}

func TestOpenMachineSnapshot_DoesNotReuseClientHTTPTimeout(t *testing.T) {
	prevIdle, prevTotal := snapshotIdleTimeout, snapshotTotalTimeout
	snapshotIdleTimeout = time.Second
	snapshotTotalTimeout = time.Second
	t.Cleanup(func() {
		snapshotIdleTimeout = prevIdle
		snapshotTotalTimeout = prevTotal
	})
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(machineSnapshotFixture(t))
	}))
	t.Cleanup(srv.Close)
	c := snapshotTestClient(srv)
	c.http.Timeout = 50 * time.Millisecond
	req, _, out := newAssembleRequest(t)
	result, err := assembleMachineSnapshot(t, c, fixtureWindow(), req)
	if err != nil {
		t.Fatalf("independent stream client reused the 10s/50ms HTTP timeout: %v", err)
	}
	assertSnapshotExportIdentity(t, result.Path, out)
	select {
	case <-started:
	default:
		t.Fatal("handler was not reached")
	}
}

func TestOpenMachineSnapshot_IdleTimeoutDoesNotPublish(t *testing.T) {
	prevIdle, prevTotal := snapshotIdleTimeout, snapshotTotalTimeout
	snapshotIdleTimeout = 50 * time.Millisecond
	snapshotTotalTimeout = time.Second
	t.Cleanup(func() {
		snapshotIdleTimeout = prevIdle
		snapshotTotalTimeout = prevTotal
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	c := snapshotTestClient(srv)
	req, parent, out := newAssembleRequest(t)
	_, err := assembleMachineSnapshot(t, c, fixtureWindow(), req)
	assertSnapshotDataFailure(t, err)
	assertNoExportResidue(t, parent, out)
}

func TestOpenMachineSnapshot_SecondRedactORsServerFlag(t *testing.T) {
	payload := []byte(`{"time":"2026-09-05T00:00:00Z","msg":"local-secret-value"}`)
	encoded := base64.StdEncoding.EncodeToString(payload)
	sum := snapshotPayloadDigest(payload)
	n := strconv.Itoa(len(payload) + 1)
	body := joinSnapshotFrames([][]byte{
		[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"header","snapshot_id":"00000000000000000000000000000001","to":"2026-09-05T00:00:00Z","sources":["daemon","mihomo"]}`),
		[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"record","source":"daemon","payload_b64":"` + encoded + `","redacted":true}`),
		[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"source_end","source":"daemon","lines":1,"skipped_invalid":0,"redacted":1,"sources":["mihari-daemon.log"],"sha256":"` + sum + `","bytes":` + n + `}`),
		[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"source_end","source":"mihomo","lines":0,"skipped_invalid":0,"redacted":0,"sources":[],"sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","bytes":0}`),
		[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"complete","snapshot_id":"00000000000000000000000000000001","source_count":2,"total_bytes":` + n + `}`),
	})
	srv := httptest.NewServer(snapshotBodyHandler(t, body, false, nil))
	t.Cleanup(srv.Close)
	c := snapshotTestClient(srv)
	if err := c.SetRedactor(logging.NewRedactor("local-secret-value")); err != nil {
		t.Fatal(err)
	}
	req, _, out := newAssembleRequest(t)
	if _, err := assembleMachineSnapshot(t, c, fixtureWindow(), req); err != nil {
		t.Fatal(err)
	}
	got := readExportZip(t, out)
	if strings.Contains(got["daemon/mihari-daemon.log"], "local-secret-value") {
		t.Fatal("local secret leaked")
	}
	manifest := decodeManifest(t, got["manifest.json"])
	files, _ := manifest["files"].([]any)
	file, _ := files[0].(map[string]any)
	if file["redacted"] != json.Number("1") {
		t.Fatalf("redacted counted twice or dropped: %v", file["redacted"])
	}
}

func TestOpenMachineSnapshot_FrameBudgetAroundBufioBuffer(t *testing.T) {
	t.Run("between-32KiB-and-2MiB", func(t *testing.T) {
		payload := largeSnapshotPayload(40 << 10)
		record := []byte(`{"schema":"mihari.machine-log-stream/v1","type":"record","source":"daemon","payload_b64":"` + base64.StdEncoding.EncodeToString(payload) + `","redacted":false}`)
		if len(record)+1 <= 32<<10 || len(record)+1 > protocol.MaxMachineLogFrameBytes {
			t.Fatalf("record frame=%d, want (32KiB, 2MiB]", len(record)+1)
		}
		sum := snapshotPayloadDigest(payload)
		n := strconv.Itoa(len(payload) + 1)
		body := joinSnapshotFrames([][]byte{
			[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"header","snapshot_id":"00000000000000000000000000000001","to":"2026-09-05T00:00:00Z","sources":["daemon","mihomo"]}`),
			record,
			[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"source_end","source":"daemon","lines":1,"skipped_invalid":0,"redacted":0,"sources":["mihari-daemon.log"],"sha256":"` + sum + `","bytes":` + n + `}`),
			[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"source_end","source":"mihomo","lines":0,"skipped_invalid":0,"redacted":0,"sources":[],"sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","bytes":0}`),
			[]byte(`{"schema":"mihari.machine-log-stream/v1","type":"complete","snapshot_id":"00000000000000000000000000000001","source_count":2,"total_bytes":` + n + `}`),
		})
		srv := httptest.NewServer(snapshotBodyHandler(t, body, false, nil))
		t.Cleanup(srv.Close)
		c := snapshotTestClient(srv)
		req, _, out := newAssembleRequest(t)
		if _, err := assembleMachineSnapshot(t, c, fixtureWindow(), req); err != nil {
			t.Fatal(err)
		}
		got := readExportZip(t, out)
		if !strings.Contains(got["daemon/mihari-daemon.log"], `"msg"`) {
			t.Fatalf("daemon log missing payload: %q", got["daemon/mihari-daemon.log"][:min(len(got["daemon/mihari-daemon.log"]), 80)])
		}
	})
	t.Run("over-2MiB", func(t *testing.T) {
		header := []byte(`{"schema":"mihari.machine-log-stream/v1","type":"header","snapshot_id":"00000000000000000000000000000001","to":"2026-09-05T00:00:00Z","sources":["daemon","mihomo"]}`)
		oversized := append(bytes.Repeat([]byte{'x'}, protocol.MaxMachineLogFrameBytes), '\n')
		if len(oversized) != protocol.MaxMachineLogFrameBytes+1 {
			t.Fatalf("oversized frame=%d", len(oversized))
		}
		body := append(joinSnapshotFrames([][]byte{header}), oversized...)
		srv := httptest.NewServer(snapshotBodyHandler(t, body, false, nil))
		t.Cleanup(srv.Close)
		c := snapshotTestClient(srv)
		req, parent, out := newAssembleRequest(t)
		_, err := assembleMachineSnapshot(t, c, fixtureWindow(), req)
		assertSnapshotDataFailure(t, err)
		assertNoExportResidue(t, parent, out)
	})
}

func largeSnapshotPayload(n int) []byte {
	prefix := `{"time":"2026-09-05T00:00:00Z","msg":"`
	suffix := `"}`
	pad := n - len(prefix) - len(suffix)
	if pad < 1 {
		pad = 1
	}
	return []byte(prefix + strings.Repeat("x", pad) + suffix)
}

func snapshotTestClient(srv *httptest.Server) *Client {
	c := NewHTTP(srv.URL, "token", srv.Client())
	c.http.Timeout = 10 * time.Second
	return c
}

func fixtureWindow() logging.SnapshotWindow {
	to := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	return logging.SnapshotWindow{To: to}
}

func assembleMachineSnapshot(t *testing.T, c *Client, window logging.SnapshotWindow, req logging.ExportRequest) (logging.ExportResult, error) {
	t.Helper()
	set, err := c.OpenMachineSnapshot(context.Background(), window)
	if err != nil {
		return logging.ExportResult{}, err
	}
	result, err := logging.Assemble(context.Background(), req, logging.ExportScopeMachineAndCurrentUser, logging.NamedSourcesFromSet(set), set.Finish)
	return result, errors.Join(err, set.Close())
}

func newAssembleRequest(t *testing.T) (logging.ExportRequest, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	paths := platform.NewPaths(root)
	fs, err := platform.NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	out := filepath.Join(parent, "export.zip")
	return logging.ExportRequest{
		Now:        time.Date(2026, 9, 5, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)),
		Range:      logging.ExportRange{Kind: logging.RangeAll},
		OutputPath: out,
		Paths: logging.ExportPaths{
			LogDir: paths.LogDir, ExportDir: paths.LogExportDir,
			DaemonLog: paths.DaemonLog, TUILog: paths.TUILog, MihomoLog: paths.MihomoLog,
		},
		PrivateFS: fs,
		Redactor:  logging.NewRedactor(),
	}, parent, out
}

func snapshotBodyHandler(t *testing.T, body []byte, hold bool, closed *atomic.Bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if closed != nil {
			defer closed.Store(true)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/logging/snapshot" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(body); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if hold {
			<-r.Context().Done()
		}
	})
}

func machineSnapshotFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", "machine-snapshot-v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 || body[len(body)-1] != '\n' {
		t.Fatal("fixture must end with LF")
	}
	return body
}

func splitSnapshotFrames(t *testing.T, body []byte) [][]byte {
	t.Helper()
	trimmed := bytes.TrimSuffix(body, []byte{'\n'})
	parts := bytes.Split(trimmed, []byte{'\n'})
	if len(parts) != 5 {
		t.Fatalf("fixture frames=%d", len(parts))
	}
	return parts
}

func joinSnapshotFrames(frames [][]byte) []byte {
	var out []byte
	for _, frame := range frames {
		out = append(out, frame...)
		out = append(out, '\n')
	}
	return out
}

func replaceFrameField(frames [][]byte, typeName, old, new string) [][]byte {
	out := make([][]byte, len(frames))
	replaced := false
	for i, frame := range frames {
		if !replaced && bytes.Contains(frame, []byte(`"type":"`+typeName+`"`)) && bytes.Contains(frame, []byte(old)) {
			out[i] = bytes.Replace(frame, []byte(old), []byte(new), 1)
			replaced = true
			continue
		}
		out[i] = append([]byte(nil), frame...)
	}
	return out
}

func assertSnapshotDataFailure(t *testing.T, err error) {
	t.Helper()
	if !isSnapshotDataFailure(err) {
		t.Fatalf("want data_failure, got %v", err)
	}
}

func isSnapshotDataFailure(err error) bool {
	var api protocol.APIError
	return errors.As(err, &api) && api.Code == protocol.CodeDataFailure
}

func assertNoExportResidue(t *testing.T, parent, out string) {
	t.Helper()
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published %s: %v", out, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("spool remains: %v", names)
	}
}

func readExportZip(t *testing.T, path string) map[string]string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got := make(map[string]string, len(r.File))
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[f.Name] = string(b)
	}
	return got
}

func decodeManifest(t *testing.T, raw string) map[string]any {
	t.Helper()
	var manifest map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func assertSnapshotExportIdentity(t *testing.T, actual, expected string) {
	t.Helper()
	got, gotErr := os.Stat(actual)
	want, wantErr := os.Stat(expected)
	if gotErr != nil || wantErr != nil || !os.SameFile(got, want) {
		t.Fatalf("published path does not identify requested export: %v / %v", gotErr, wantErr)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func snapshotPayloadDigest(payload []byte) string {
	sum := sha256.Sum256(append(append([]byte(nil), payload...), '\n'))
	return hex.EncodeToString(sum[:])
}
