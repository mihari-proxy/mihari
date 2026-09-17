package connections

import (
	"fmt"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestHistory_DefaultRetains5000AndEvictsOldest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		history *History
	}{
		{"zero", NewHistory(0)}, {"negative", NewHistory(-1)}, {"page", New(nil, nil).history},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.history
			active := make([]protocol.Connection, 5001)
			for i := range active {
				active[i].ID = fmt.Sprintf("conn-%04d", i)
			}
			h.Observe(active, time.Unix(1, 0))
			if len(h.Active()) != 5001 {
				t.Fatal("history quota limited active connections")
			}
			h.Observe(active[501:], time.Unix(2, 0))
			if got := len(h.Closed()); got != 501 {
				t.Fatalf("closed=%d want 501", got)
			}
			h.Observe(active[5000:], time.Unix(3, 0))
			closed := h.Closed()
			if len(closed) != 5000 || closed[0].ID != "conn-0000" {
				t.Fatalf("before overflow: count=%d", len(closed))
			}
			h.Observe(nil, time.Unix(4, 0))
			closed = h.Closed()
			if len(closed) != 5000 || closed[0].ID != "conn-0001" || closed[4999].ID != "conn-5000" {
				t.Fatalf("after overflow: count=%d first=%q last=%q", len(closed), closed[0].ID, closed[len(closed)-1].ID)
			}
			h.Reset()
			if len(h.Closed()) != 0 || len(h.Active()) != 0 {
				t.Fatal("reset retained history")
			}
		})
	}
}
