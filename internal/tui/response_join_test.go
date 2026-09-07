package tui

import (
	"testing"
	"time"
)

func TestCloseResponse_JoinsOwnerAfterTimeout(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { closeResponseAndJoin(func() { close(entered); <-release }); close(done) }()
	<-entered
	select {
	case <-done:
		close(release)
		t.Fatal("response owner was abandoned before filesystem cleanup")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	<-done
}
