package platform

import (
	"context"
	"sync"
)

type capabilityLifetime struct {
	mu        sync.Mutex
	closed    bool
	operation chan struct{}
	closing   chan struct{}
	closeDone chan struct{}
}

func (fs *capabilityLifetime) closeWith(closePlatform func() error) error {
	fs.mu.Lock()
	if fs.closed {
		done := fs.closeDone
		fs.mu.Unlock()
		<-done
		return nil
	}
	fs.closed = true
	fs.closeDone = make(chan struct{})
	done, operation := fs.closeDone, fs.operation
	if fs.closing != nil {
		close(fs.closing)
	}
	fs.mu.Unlock()
	if operation != nil {
		<-operation
	}
	err := closePlatform()
	close(done)
	return err
}

// begin serializes operations without holding a mutex during filesystem IO.
func (fs *capabilityLifetime) begin(ctx context.Context) (func(), error) {
	fs.mu.Lock()
	if fs.closed {
		fs.mu.Unlock()
		return nil, errPrivateFSClosed
	}
	if fs.operation == nil {
		fs.operation = make(chan struct{}, 1)
		fs.operation <- struct{}{}
		fs.closing = make(chan struct{})
	}
	operation, closing := fs.operation, fs.closing
	fs.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-closing:
		return nil, errPrivateFSClosed
	case <-operation:
	}
	fs.mu.Lock()
	closed := fs.closed
	fs.mu.Unlock()
	if closed {
		operation <- struct{}{}
		return nil, errPrivateFSClosed
	}
	if err := ctx.Err(); err != nil {
		operation <- struct{}{}
		return nil, err
	}
	return func() { operation <- struct{}{} }, nil
}
