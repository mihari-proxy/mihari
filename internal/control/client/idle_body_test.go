package client

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type immediateIdleBody struct {
	closed chan struct{}
	once   sync.Once
}

type failingIdleBody struct {
	closed chan struct{}
	once   sync.Once
	closes atomic.Int32
	err    error
}

func (b *failingIdleBody) Read([]byte) (int, error) { <-b.closed; return 0, io.EOF }
func (b *failingIdleBody) Close() error {
	b.closes.Add(1)
	b.once.Do(func() { close(b.closed) })
	return b.err
}

func TestIdleBody_TimeoutPreservesCloseCauseOnce(t *testing.T) {
	cause := errors.New("close idle snapshot fixture")
	source := &failingIdleBody{closed: make(chan struct{}), err: cause}
	body := newIdleBody(source, 0)
	select {
	case <-source.closed:
	case <-time.After(time.Second):
		t.Fatal("idle timeout did not close response")
	}
	if err := body.Close(); !errors.Is(err, cause) {
		t.Fatalf("close cause lost: %v", err)
	}
	if got := source.closes.Load(); got != 1 {
		t.Fatalf("response closed %d times", got)
	}
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
