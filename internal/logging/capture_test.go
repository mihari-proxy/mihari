package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestLineCaptureWriter_MultipleLinesOneWrite(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	n, err := w.Write([]byte("one\ntwo\nthree\n"))
	if err != nil || n != 14 {
		t.Fatalf("Write n=%d err=%v", n, err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 3 {
		t.Fatalf("records=%d, want 3 in %q", len(recs), buf.String())
	}
	assertCapture(t, recs[0], "INFO", "one", "stdout", false, false)
	assertCapture(t, recs[1], "INFO", "two", "stdout", false, false)
	assertCapture(t, recs[2], "INFO", "three", "stdout", false, false)
}

func TestLineCaptureWriter_SplitAcrossWrites(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	if n, err := w.Write([]byte("hel")); err != nil || n != 3 {
		t.Fatalf("Write n=%d err=%v", n, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("partial write emitted %q", buf.String())
	}
	if n, err := w.Write([]byte("lo\n")); err != nil || n != 3 {
		t.Fatalf("Write n=%d err=%v", n, err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	assertCapture(t, recs[0], "INFO", "hello", "stdout", false, false)
}

func TestLineCaptureWriter_CRLF(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	if _, err := w.Write([]byte("hello\r\nworld\r\n")); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 2 {
		t.Fatalf("records=%d, want 2", len(recs))
	}
	assertCapture(t, recs[0], "INFO", "hello", "stdout", false, false)
	assertCapture(t, recs[1], "INFO", "world", "stdout", false, false)
}

func TestLineCaptureWriter_EmptyLine(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	if _, err := w.Write([]byte("a\n\nb\n")); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 3 {
		t.Fatalf("records=%d, want 3", len(recs))
	}
	assertCapture(t, recs[1], "INFO", "", "stdout", false, false)
}

func TestLineCaptureWriter_InvalidUTF8(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	if _, err := w.Write([]byte{0xff, 0xfe, '\n'}); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	assertCapture(t, recs[0], "INFO", "\uFFFD\uFFFD", "stdout", false, true)
}

func TestLineCaptureWriter_UTF8SplitAcrossWrites(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	world := []byte("世界")
	if _, err := w.Write(world[:2]); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("incomplete rune emitted %q", buf.String())
	}
	if _, err := w.Write(append(world[2:], '\n')); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	assertCapture(t, recs[0], "INFO", "世界", "stdout", false, false)
}

func TestLineCaptureWriter_Exact16KiB(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	line := bytes.Repeat([]byte("x"), MaxCaptureLineBytes)
	payload := append(line, '\n')
	n, err := w.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("Write n=%d err=%v", n, err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	assertCapture(t, recs[0], "INFO", string(line), "stdout", false, false)
}

func TestLineCaptureWriter_TruncatesOver16KiB(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	line := bytes.Repeat([]byte("x"), MaxCaptureLineBytes+100)
	payload := append(append(line, '\n'), []byte("next\n")...)
	n, err := w.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("Write n=%d err=%v", n, err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 2 {
		t.Fatalf("records=%d, want 2", len(recs))
	}
	assertCapture(t, recs[0], "INFO", strings.Repeat("x", MaxCaptureLineBytes), "stdout", true, false)
	assertCapture(t, recs[1], "INFO", "next", "stdout", false, false)
}

func TestLineCaptureWriter_BufferNeverExceedsExactCap(t *testing.T) {
	w, output := newTestCapture(t, slog.LevelInfo, "stdout")
	impl := w.(*lineCaptureWriter)
	lead := bytes.Repeat([]byte("x"), MaxCaptureLineBytes-1)
	rune3 := []byte("世")
	rest := bytes.Repeat([]byte("y"), 64)
	payload := append(append(lead, rune3...), rest...)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	impl.mu.Lock()
	n := len(impl.buf)
	impl.mu.Unlock()
	if n > MaxCaptureLineBytes {
		t.Fatalf("buf len=%d exceeds MaxCaptureLineBytes", n)
	}
	if n < MaxCaptureLineBytes {
		t.Fatalf("buf len=%d, want at least %d", n, MaxCaptureLineBytes)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, output.String())
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	assertCapture(t, recs[0], "INFO", strings.Repeat("x", MaxCaptureLineBytes-1)+"\uFFFD", "stdout", true, true)
}

func TestLineCaptureWriter_FlushPartialThenWrite(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	if _, err := w.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("cd")); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 2 {
		t.Fatalf("records=%d, want 2", len(recs))
	}
	assertCapture(t, recs[0], "INFO", "ab", "stdout", false, false)
	assertCapture(t, recs[1], "INFO", "cd", "stdout", false, false)
}

func TestLineCaptureWriter_FlushSurvivesChildLifetimes(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	if _, err := w.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("next-start\n")); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 2 {
		t.Fatalf("records=%d want 2 (must not glue child lifetimes): %q", len(recs), buf.String())
	}
	assertCapture(t, recs[0], "INFO", "partial", "stdout", false, false)
	assertCapture(t, recs[1], "INFO", "next-start", "stdout", false, false)
}

func TestLineCaptureWriter_CloseFlushesPartial(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, newDebugLevel(), "mihomo", NewRedactor()))
	w := NewLineCaptureWriter(logger, slog.LevelInfo, "stdout")
	if _, err := w.Write([]byte("no-nl")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1 after idempotent Close", len(recs))
	}
	assertCapture(t, recs[0], "INFO", "no-nl", "stdout", false, false)
	logger.Info("after-close")
	if !strings.Contains(buf.String(), `"msg":"after-close"`) {
		t.Fatalf("Close must not close shared logger: %s", buf.String())
	}
}

func TestLineCaptureWriter_WriteAfterClose(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	before := buf.String()
	n, err := w.Write([]byte("late\n"))
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("err=%v, want os.ErrClosed", err)
	}
	if n != 0 {
		t.Fatalf("n=%d, want 0", n)
	}
	if buf.String() != before {
		t.Fatalf("Write after Close touched logger: %q", buf.String())
	}
	if err := w.Flush(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Flush after Close err=%v, want os.ErrClosed", err)
	}
}

func TestLineCaptureWriter_StdoutInfoStderrWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, newDebugLevel(), "mihomo", NewRedactor()))
	out := NewLineCaptureWriter(logger, slog.LevelInfo, "stdout")
	errw := NewLineCaptureWriter(logger, slog.LevelWarn, "stderr")
	t.Cleanup(func() {
		_ = out.Close()
		_ = errw.Close()
	})
	if _, err := out.Write([]byte("from-out\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := errw.Write([]byte("from-err\n")); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 2 {
		t.Fatalf("records=%d, want 2", len(recs))
	}
	assertCapture(t, recs[0], "INFO", "from-out", "stdout", false, false)
	assertCapture(t, recs[1], "WARN", "from-err", "stderr", false, false)
}

func TestLineCaptureWriter_JSONHasComponentStreamNoSource(t *testing.T) {
	w, buf := newTestCapture(t, slog.LevelInfo, "stdout")
	if _, err := w.Write([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}
	recs := parseCaptureJSONL(t, buf.String())
	if len(recs) != 1 {
		t.Fatalf("records=%d, want 1", len(recs))
	}
	if recs[0]["component"] != "mihomo" {
		t.Fatalf("component=%v", recs[0]["component"])
	}
	if _, ok := recs[0]["source"]; ok {
		t.Fatalf("source key must not appear: %v", recs[0])
	}
}

func TestLineCaptureWriter_WriteIgnoresLoggerErrors(t *testing.T) {
	logger := slog.New(failHandler{})
	w := NewLineCaptureWriter(logger, slog.LevelInfo, "stdout")
	payload := []byte("boom\nand-more\n")
	n, err := w.Write(payload)
	if err != nil {
		t.Fatalf("Write returned logger error: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("n=%d, want %d", n, len(payload))
	}
}

func TestLineCaptureWriter_LevelFilterStillAcceptsWrite(t *testing.T) {
	var buf bytes.Buffer
	level := &slog.LevelVar{}
	level.Set(slog.LevelWarn)
	logger := slog.New(NewJSONHandler(&buf, level, "mihomo", NewRedactor()))
	w := NewLineCaptureWriter(logger, slog.LevelInfo, "stdout")
	t.Cleanup(func() { _ = w.Close() })
	payload := []byte("filtered\n")
	n, err := w.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("Write n=%d err=%v", n, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("filtered line was written: %q", buf.String())
	}
}

func newTestCapture(t *testing.T, level slog.Level, stream string) (LineCaptureWriter, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, newDebugLevel(), "mihomo", NewRedactor()))
	w := NewLineCaptureWriter(logger, level, stream)
	t.Cleanup(func() { _ = w.Close() })
	return w, &buf
}

func newDebugLevel() *slog.LevelVar {
	level := &slog.LevelVar{}
	level.Set(slog.LevelDebug)
	return level
}

func parseCaptureJSONL(t *testing.T, raw string) []map[string]any {
	t.Helper()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("json: %v in %q", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func assertCapture(t *testing.T, rec map[string]any, level, msg, stream string, truncated, invalid bool) {
	t.Helper()
	if rec["level"] != level {
		t.Fatalf("level=%v want %s", rec["level"], level)
	}
	if rec["msg"] != msg {
		t.Fatalf("msg=%q want %q", rec["msg"], msg)
	}
	if rec["stream"] != stream {
		t.Fatalf("stream=%v want %s", rec["stream"], stream)
	}
	if rec["component"] != "mihomo" {
		t.Fatalf("component=%v", rec["component"])
	}
	if _, ok := rec["source"]; ok {
		t.Fatalf("source present: %v", rec)
	}
	if _, ok := rec["truncated"]; ok != truncated {
		t.Fatalf("truncated presence=%v want %v in %v", ok, truncated, rec)
	}
	if truncated {
		if rec["truncated"] != true {
			t.Fatalf("truncated=%v", rec["truncated"])
		}
	}
	if _, ok := rec["invalid_utf8"]; ok != invalid {
		t.Fatalf("invalid_utf8 presence=%v want %v in %v", ok, invalid, rec)
	}
	if invalid && rec["invalid_utf8"] != true {
		t.Fatalf("invalid_utf8=%v", rec["invalid_utf8"])
	}
}

type failHandler struct{}

func (failHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (failHandler) Handle(context.Context, slog.Record) error { return errors.New("disk full") }
func (failHandler) WithAttrs([]slog.Attr) slog.Handler        { return failHandler{} }
func (failHandler) WithGroup(string) slog.Handler             { return failHandler{} }
