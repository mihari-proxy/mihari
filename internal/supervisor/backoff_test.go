package supervisor

import (
	"testing"
	"time"
)

func TestBackoffSequenceCapsAndResets(t *testing.T) {
	backoff := NewBackoff(time.Second, 30*time.Second)
	wants := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for index, want := range wants {
		if got := backoff.Next(); got != want {
			t.Fatalf("next[%d]=%s want=%s", index, got, want)
		}
	}
	backoff.Reset()
	if got := backoff.Next(); got != time.Second {
		t.Fatalf("after reset=%s", got)
	}
}
