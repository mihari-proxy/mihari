package tundetect

import (
	"context"
	"errors"
	"testing"
)

func TestFakeBackend_PropagatesErrors(t *testing.T) {
	backend := &FakeBackend{Err: errors.New("detect failed")}
	got, err := backend.Detect(context.Background())
	if err == nil || err.Error() != "detect failed" || backend.DetectCalls != 1 {
		t.Fatalf("got=%#v err=%v calls=%d", got, err, backend.DetectCalls)
	}
}
