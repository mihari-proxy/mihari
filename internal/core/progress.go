package core

import (
	"context"
	"io"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// Progress is one observation of a core install phase.
// Total is the compressed release asset size; zero means the publisher did not give one.
type Progress struct {
	Phase    string
	Received int64
	Total    int64
}

type progressReporterKey struct{}

// WithProgressReporter returns a context the core installer uses to publish phase changes.
func WithProgressReporter(ctx context.Context, report func(Progress)) context.Context {
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, progressReporterKey{}, report)
}

// ReportProgress publishes progress when the context carries a reporter.
func ReportProgress(ctx context.Context, progress Progress) {
	report, _ := ctx.Value(progressReporterKey{}).(func(Progress))
	if report != nil {
		report(progress)
	}
}

func reportPhase(ctx context.Context, phase string) {
	ReportProgress(ctx, Progress{Phase: phase})
}

// progressReader counts bytes copied from a release asset and publishes them.
type progressReader struct {
	ctx   context.Context
	src   io.Reader
	total int64
	read  int64
}

func newProgressReader(ctx context.Context, src io.Reader, total int64) *progressReader {
	ReportProgress(ctx, Progress{Phase: protocol.ProgressPhaseDownloading, Total: total})
	return &progressReader{ctx: ctx, src: src, total: total}
}

func (r *progressReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if n > 0 {
		r.read += int64(n)
		ReportProgress(r.ctx, Progress{Phase: protocol.ProgressPhaseDownloading, Received: r.read, Total: r.total})
	}
	return n, err
}
