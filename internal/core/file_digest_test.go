package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestDigestCoreFile_SizeAndContent(t *testing.T) {
	for _, size := range []int{0, 1, 32768, 32769, 65536} {
		content := bytes.Repeat([]byte{'x'}, size)
		got, n, err := digestCoreFile(t.Context(), bytes.NewReader(content), int64(size))
		if err != nil || n != int64(size) || got != digest(content) {
			t.Fatalf("size=%d hash=%q read=%d err=%v", size, got, n, err)
		}
	}
	reader := bytes.NewReader(make([]byte, 65536))
	if hash, n, err := digestCoreFile(t.Context(), reader, 32768); err == nil || hash != "" || n != 32769 || reader.Len() != 32767 {
		t.Fatalf("oversize input was not bounded: hash=%q read=%d remaining=%d err=%v", hash, n, reader.Len(), err)
	}
}

func TestDigestCoreFile_ReadFailure(t *testing.T) {
	cause := errors.New("synthetic executable read failure")
	reader := &digestTestReader{read: func([]byte) (int, error) { return 0, cause }}
	if hash, _, err := digestCoreFile(t.Context(), reader, 65536); hash != "" || !errors.Is(err, cause) {
		t.Fatalf("hash=%q err=%v", hash, err)
	}
}

func TestDigestCoreFile_CancellationStopsBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	reader := &digestTestReader{read: func(b []byte) (int, error) {
		calls++
		if len(b) > 32768 {
			t.Fatalf("unbounded read: %d", len(b))
		}
		cancel()
		return len(b), nil
	}}
	if hash, n, err := digestCoreFile(ctx, reader, 65536); hash != "" || n != 32768 || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("hash=%q read=%d err=%v calls=%d", hash, n, err, calls)
	}
	if _, _, err := digestCoreFile(ctx, reader, 65536); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("already canceled probe read the file: err=%v calls=%d", err, calls)
	}
}

func TestDigestCoreFile_CancellationAtEOFDoesNotPublishDigest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader := &digestTestReader{read: func([]byte) (int, error) { cancel(); return 0, io.EOF }}
	if hash, _, err := digestCoreFile(ctx, reader, 65536); hash != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("hash=%q err=%v", hash, err)
	}
}

type digestTestReader struct{ read func([]byte) (int, error) }

func (r *digestTestReader) Read(b []byte) (int, error) { return r.read(b) }
