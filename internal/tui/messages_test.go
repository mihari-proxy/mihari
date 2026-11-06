package tui

import (
	"fmt"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"testing"
)

func TestWaitSessionEvent_PreservesReadyOrderAndBound(t *testing.T) {
	for _, count := range []int{0, 1, 3, 40} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			events := make(chan session.Event, 40)
			for i := range count {
				events <- session.Event{Kind: session.EventLog, Epoch: uint64(i)}
			}
			close(events)
			seen := 0
			for {
				msg := waitSessionEvent(events)().(sessionEventMsg)
				if !msg.Open {
					break
				}
				batch := msg.Event.Batch
				if len(batch) == 0 {
					batch = []session.Event{msg.Event}
				}
				if len(batch) > 32 {
					t.Fatal("batch exceeded bound")
				}
				for _, event := range batch {
					if event.Epoch != uint64(seen) {
						t.Fatal("events reordered")
					}
					seen++
				}
			}
			if seen != count {
				t.Fatalf("got %d events", seen)
			}
		})
	}
}
