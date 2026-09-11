package client

import (
	"io"
	"sync"
	"testing"
	"time"
)

type immediateIdleBody struct {
	closed chan struct{}
	once   sync.Once
}

func (b *immediateIdleBody) Read([]byte) (int, error) { <-b.closed; return 0, io.EOF }
func (b *immediateIdleBody) Close() error             { b.once.Do(func() { close(b.closed) }); return nil }
func TestIdleBody_ImmediateTimeoutOwnsPublishedTimer(t *testing.T) {
	for n := 0; n < 100; n++ {
		source := &immediateIdleBody{closed: make(chan struct{})}
		body := newIdleBody(source, 0)
		select {
		case <-source.closed:
		case <-time.After(time.Second):
			_ = body.Close()
			t.Fatal("timeout callback ran before its timer was published")
		}
		if err := body.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
