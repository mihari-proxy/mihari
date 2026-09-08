package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
)

func readSourceContent(ctx context.Context, reader io.Reader, limit int64, retain bool) ([]byte, int64, string, error) {
	hash := sha256.New()
	var raw bytes.Buffer
	var output io.Writer = hash
	if retain {
		output = io.MultiWriter(&raw, hash)
	}
	size, err := io.Copy(output, io.LimitReader(&sourceContextReader{ctx: ctx, r: reader}, limit+1))
	if err != nil {
		return nil, size, "", err
	}
	return raw.Bytes(), size, hex.EncodeToString(hash.Sum(nil)), nil
}

type sourceContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *sourceContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
