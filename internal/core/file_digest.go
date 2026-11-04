package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// digestCoreFile retains no executable bytes. The reader is bounded and checks
// cancellation between chunks, including when the input is already cached.
func digestCoreFile(ctx context.Context, reader io.Reader, limit int64) (string, int64, error) {
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	size, err := io.CopyBuffer(hash, io.LimitReader(contextReader{ctx: ctx, r: reader}, limit+1), buffer)
	if err != nil {
		return "", size, err
	}
	if err := ctx.Err(); err != nil {
		return "", size, err
	}
	if size > limit {
		return "", size, dataFailure("core object exceeds size limit")
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if len(b) > 32768 {
		b = b[:32768]
	}
	return r.r.Read(b)
}
