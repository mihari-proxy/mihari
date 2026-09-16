package diagnostics

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func testHistory(t *testing.T, records, bytes int) *History {
	t.Helper()
	history, err := NewHistory(HistoryOptions{InstanceID: "fixture-instance", MaxRecords: records, MaxBytes: bytes, Now: func() time.Time { return time.Unix(123, 0).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	return history
}

func TestHistory_RecordLimitAndExplicitExpiry(t *testing.T) {
	history := testHistory(t, 2, 1024)
	first := history.Add(protocol.Diagnostic{Detail: "token=fixture-one"})
	second := history.Add(protocol.Diagnostic{Detail: "token=fixture-two"})
	third := history.Add(protocol.Diagnostic{Detail: "token=fixture-two"})
	if second.ID == third.ID {
		t.Fatal("different occurrences were merged")
	}
	page := history.List("fixture-instance", 0, 1)
	if len(page.Records) != 1 || page.Records[0].ID != second.ID || !page.LostBefore || !page.HasMore || page.NextSequence != second.Sequence {
		t.Fatalf("bounded first page = %+v", page)
	}
	if page.Records[0].Detail != "" || page.Records[0].State != protocol.DiagnosticReference {
		t.Fatal("metadata list contained a body")
	}
	if got := history.Get(first.ID); got.State != protocol.DiagnosticExpired || got.Diagnostic != nil {
		t.Fatalf("evicted result = %+v", got)
	}
	next := history.List("fixture-instance", page.NextSequence, 1)
	if len(next.Records) != 1 || next.Records[0].ID != third.ID || next.HasMore || next.LostBefore {
		t.Fatalf("next page = %+v", next)
	}
}

func TestHistory_ByteLimitDoesNotAlterReturnedOccurrence(t *testing.T) {
	history := testHistory(t, 100, 250)
	first := history.Add(protocol.Diagnostic{Detail: strings.Repeat("a", 100)})
	second := history.Add(protocol.Diagnostic{Detail: strings.Repeat("b", 100)})
	if history.Get(first.ID).State != protocol.DiagnosticExpired || history.Get(second.ID).State != protocol.DiagnosticAvailable {
		t.Fatal("byte budget did not evict only the oldest record")
	}
	large := history.Add(protocol.Diagnostic{Detail: strings.Repeat("c", 400)})
	if large.Detail != strings.Repeat("c", 400) || history.Get(large.ID).State != protocol.DiagnosticExpired {
		t.Fatal("oversized history record must remain intact in its direct outcome and expire from history")
	}
}

func TestHistory_RestartUnknownAndImmutableCopies(t *testing.T) {
	history := testHistory(t, 2, 1024)
	record := history.Add(protocol.Diagnostic{Detail: "password=original"})
	restarted := history.List("old-instance", 1000, 50)
	if restarted.State != protocol.DiagnosticRestarted || len(restarted.Records) != 1 || restarted.NextSequence != record.Sequence {
		t.Fatalf("restarted page = %+v", restarted)
	}
	for _, id := range []string{"invalid", "fixture-instance:0", "fixture-instance:invalid", "fixture-instance:999"} {
		if got := history.Get(id); got.State != protocol.DiagnosticUnknown {
			t.Fatalf("unknown %q = %+v", id, got)
		}
	}
	if got := history.Get("old-instance:1"); got.State != protocol.DiagnosticRestarted {
		t.Fatalf("old instance = %+v", got)
	}
	copy := history.Get(record.ID)
	copy.Diagnostic.Detail = "changed"
	if history.Get(record.ID).Diagnostic.Detail != "password=original" || record.Time != time.Unix(123, 0).UTC() {
		t.Fatal("snapshot was mutable or clock not injected")
	}
}

func TestHistory_ConcurrentOccurrenceIdentity(t *testing.T) {
	history := testHistory(t, 256, 32<<20)
	var work sync.WaitGroup
	for worker := range 8 {
		work.Add(1)
		go func() {
			defer work.Done()
			for index := range 16 {
				record := history.Add(protocol.Diagnostic{Detail: fmt.Sprintf("worker %d item %d", worker, index)})
				_ = history.Get(record.ID)
				_ = history.List("fixture-instance", 0, 10)
			}
		}()
	}
	work.Wait()
	first := history.List("fixture-instance", 0, 100)
	second := history.List("fixture-instance", first.NextSequence, 100)
	if first.LatestSequence != 128 || len(first.Records)+len(second.Records) != 128 {
		t.Fatal("concurrent occurrences were lost")
	}
}
