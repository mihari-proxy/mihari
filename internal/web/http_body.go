package web

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// observedHTTPBody observes only bytes consumed by the existing reverse proxy.
// Close owns the single final record, including a partial read or close failure.
type observedHTTPBody struct {
	io.ReadCloser
	ctx       context.Context
	reporter  diagnostics.Reporter
	detail    diagnostics.HTTPError
	raw       []byte
	secret    string
	truncated bool
	closed    bool
}

func (b *observedHTTPBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	const limit = 64 << 10
	if b.detail.Status >= 400 {
		size := min(n, limit-len(b.raw))
		b.raw = append(b.raw, p[:size]...)
		if size < n {
			b.truncated = true
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		b.detail.Cause = errors.Join(b.detail.Cause, err)
		if b.detail.Status < 400 {
			b.detail.Phase = "read"
		}
	}
	return n, err
}

func (b *observedHTTPBody) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	err := b.ReadCloser.Close()
	closeOnly := b.detail.Status < 400 && b.detail.Cause == nil && err != nil
	b.detail.Cause = errors.Join(b.detail.Cause, err)
	b.detail.Body = diagnostics.HTTPBody(b.raw, b.secret)
	if b.truncated {
		b.detail.Body += " [truncated]"
	}
	// This mutates the diagnostic copy; the return value only supports chaining.
	_ = b.detail.HideSecret(b.secret)
	if closeOnly && b.reporter != nil {
		b.detail.Phase = "close"
		b.reporter(b.ctx, diagnostics.Record{Component: "web", Event: "proxy.close.failed", Level: slog.LevelWarn, Err: &b.detail})
	} else if b.detail.Status >= 400 || b.detail.Cause != nil {
		reportFailure(b.ctx, b.reporter, "proxy.failed", &b.detail)
	}
	return err
}
