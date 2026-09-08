package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

type boundedSourceReader struct {
	reader    io.Reader
	read      int
	afterRead func()
}

func (r *boundedSourceReader) Read(p []byte) (int, error) {
	if len(p) > 32<<10 {
		return 0, errors.New("source hashing requested an unbounded chunk")
	}
	n, err := r.reader.Read(p)
	r.read += n
	if r.afterRead != nil {
		r.afterRead()
	}
	return n, err
}

func TestSourceContent_HashStreamsBoundedChunks(t *testing.T) {
	content := bytes.Repeat([]byte("source-content"), 1<<17)
	reader := &boundedSourceReader{reader: bytes.NewReader(content)}
	raw, size, hash, err := readSourceContent(context.Background(), reader, int64(len(content)), false)
	want := sha256.Sum256(content)
	if err != nil || raw != nil || size != int64(len(content)) || hash != hex.EncodeToString(want[:]) {
		t.Fatalf("metadata hash did not stream: size=%d retained=%v err=%v", size, raw != nil, err)
	}
}

func TestSourceContent_BoundsAndCancellation(t *testing.T) {
	for _, retain := range []bool{false, true} {
		reader := bytes.NewReader([]byte("larger than the bound"))
		raw, size, _, err := readSourceContent(context.Background(), reader, 4, retain)
		if err != nil || size != 5 || reader.Len() != len("larger than the bound")-5 {
			t.Fatalf("read beyond limit plus one: size=%d err=%v", size, err)
		}
		if retain && string(raw) != "large" || !retain && raw != nil {
			t.Fatal("content retention differs from the requested operation")
		}
		ctx, cancel := context.WithCancel(context.Background())
		chunked := &boundedSourceReader{reader: bytes.NewReader(make([]byte, 1<<20)), afterRead: cancel}
		_, _, _, err = readSourceContent(ctx, chunked, 1<<20, retain)
		cancel()
		if !errors.Is(err, context.Canceled) || chunked.read > 32<<10 {
			t.Fatalf("hashing ignored cancellation: read=%d err=%v", chunked.read, err)
		}
	}
}
