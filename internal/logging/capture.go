package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// LineCaptureWriter splits arbitrary stdout/stderr chunks into JSONL records.
type LineCaptureWriter interface {
	io.WriteCloser
	Flush() error
}

type lineCaptureWriter struct {
	logger *slog.Logger
	level  slog.Level
	stream string

	mu     sync.Mutex
	buf    []byte
	trunc  bool
	drop   bool
	closed bool
}

// NewLineCaptureWriter returns a writer that logs each logical line at level
// with stream set. Write returns len(p), nil unless the writer is closed.
func NewLineCaptureWriter(logger *slog.Logger, level slog.Level, stream string) LineCaptureWriter {
	return &lineCaptureWriter{
		logger: logger,
		level:  level,
		stream: stream,
		buf:    make([]byte, 0, MaxCaptureLineBytes+utf8.UTFMax),
	}
}

func (w *lineCaptureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	w.consume(p)
	return len(p), nil
}

func (w *lineCaptureWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	w.emitPartial()
	return nil
}

func (w *lineCaptureWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.emitPartial()
	w.closed = true
	return nil
}

func (w *lineCaptureWriter) consume(p []byte) {
	for len(p) > 0 {
		if w.drop {
			i := bytes.IndexByte(p, '\n')
			if i < 0 {
				return
			}
			w.emitLine()
			p = p[i+1:]
			continue
		}
		i := bytes.IndexByte(p, '\n')
		chunk := p
		var rest []byte
		hasNL := i >= 0
		if hasNL {
			chunk = p[:i]
			rest = p[i+1:]
		}
		overflow := false
		w.buf, overflow = appendCapped(w.buf, chunk)
		if overflow {
			w.trunc = true
			w.drop = true
			if !hasNL {
				return
			}
			w.emitLine()
			p = rest
			continue
		}
		if hasNL {
			w.emitLine()
			p = rest
			continue
		}
		return
	}
}

func (w *lineCaptureWriter) emitPartial() {
	if len(w.buf) == 0 && !w.trunc {
		w.drop = false
		return
	}
	w.emitLine()
}

func (w *lineCaptureWriter) emitLine() {
	line := w.buf
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	truncated := w.trunc
	msg, invalid := sanitizeUTF8(line)
	w.buf = w.buf[:0]
	w.trunc = false
	w.drop = false
	w.logLine(msg, truncated, invalid)
}

func (w *lineCaptureWriter) logLine(msg string, truncated, invalid bool) {
	if w.logger == nil {
		return
	}
	h := w.logger.Handler()
	if h == nil || !h.Enabled(context.Background(), w.level) {
		return
	}
	rec := slog.NewRecord(time.Now(), w.level, msg, 0)
	rec.AddAttrs(slog.String("stream", w.stream))
	if truncated {
		rec.AddAttrs(slog.Bool("truncated", true))
	}
	if invalid {
		rec.AddAttrs(slog.Bool("invalid_utf8", true))
	}
	_ = h.Handle(context.Background(), rec)
}

func appendCapped(buf, chunk []byte) ([]byte, bool) {
	room := MaxCaptureLineBytes - len(buf)
	if room < 0 {
		room = 0
	}
	if len(chunk) <= room {
		return append(buf, chunk...), false
	}
	buf = append(buf, chunk[:room]...)
	rest := chunk[room:]
	for len(rest) > 0 && len(buf) < MaxCaptureLineBytes+utf8.UTFMax && incompleteTrailing(buf) {
		buf = append(buf, rest[0])
		rest = rest[1:]
	}
	return buf, true
}

func incompleteTrailing(p []byte) bool {
	if len(p) == 0 {
		return false
	}
	i := len(p) - 1
	for i > 0 && !utf8.RuneStart(p[i]) && len(p)-i < utf8.UTFMax {
		i--
	}
	if !utf8.RuneStart(p[i]) {
		return false
	}
	need := utf8LeadSize(p[i])
	return need > len(p)-i
}

func utf8LeadSize(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b < 0xC0:
		return 1
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	case b < 0xF8:
		return 4
	default:
		return 1
	}
}

func sanitizeUTF8(b []byte) (string, bool) {
	if utf8.Valid(b) {
		return string(b), false
	}
	var out strings.Builder
	out.Grow(len(b) + utf8.UTFMax)
	invalid := false
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if size == 0 {
			break
		}
		if r == utf8.RuneError && size == 1 {
			invalid = true
			out.WriteRune(utf8.RuneError)
			i++
			continue
		}
		out.WriteRune(r)
		i += size
	}
	return out.String(), invalid
}
