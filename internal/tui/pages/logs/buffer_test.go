package logs

import (
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestBuffer_PauseCountsUnreadAndResumeShowsNewestSnapshot(t *testing.T) {
	buffer := NewBuffer(100)
	buffer.Append(entry("before"))
	buffer.Pause()
	buffer.Append(entry("one"))
	buffer.Append(entry("two"))
	if buffer.Unread() != 2 || len(buffer.Visible()) != 1 {
		t.Fatalf("unread=%d visible=%#v", buffer.Unread(), buffer.Visible())
	}
	buffer.Resume()
	if buffer.Unread() != 0 || len(buffer.Visible()) != 3 || buffer.Visible()[2].Log.Message != "two" {
		t.Fatalf("unread=%d visible=%#v", buffer.Unread(), buffer.Visible())
	}
}

func TestBuffer_OverflowReportsDroppedEntriesAndKeepsNewest(t *testing.T) {
	buffer := NewBuffer(2)
	buffer.Append(entry("one"))
	buffer.Append(entry("two"))
	buffer.Append(entry("three"))
	visible := buffer.Visible()
	if buffer.Dropped() != 1 || len(visible) != 2 || visible[0].Log.Message != "two" || visible[1].Log.Message != "three" {
		t.Fatalf("dropped=%d visible=%#v", buffer.Dropped(), visible)
	}
}

func TestModel_DefaultBufferCapacityIsTenThousand(t *testing.T) {
	model := New(0)
	for index := range 10_001 {
		model.Append(entry(string(rune('a' + index%26))))
	}
	if model.buffer.Dropped() != 1 || len(model.buffer.Visible()) != 10_000 {
		t.Fatalf("dropped=%d size=%d", model.buffer.Dropped(), len(model.buffer.Visible()))
	}
}

func entry(message string) Entry {
	return Entry{ObservedAt: time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC), Log: protocol.LogEntry{Level: "info", Message: message}}
}
