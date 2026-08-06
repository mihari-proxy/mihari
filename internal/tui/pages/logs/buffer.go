package logs

import (
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type Entry struct {
	ObservedAt time.Time
	Log        protocol.LogEntry
}

type Buffer struct {
	capacity int
	entries  []Entry
	start    int
	size     int
	paused   bool
	snapshot []Entry
	unread   int
	dropped  uint64
}

func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 1
	}
	return &Buffer{capacity: capacity, entries: make([]Entry, capacity)}
}

func (b *Buffer) Append(entry Entry) bool {
	dropped := b.size == b.capacity
	if dropped {
		b.entries[b.start] = entry
		b.start = (b.start + 1) % b.capacity
		b.dropped++
	} else {
		position := (b.start + b.size) % b.capacity
		b.entries[position] = entry
		b.size++
	}
	if b.paused {
		b.unread++
	}
	return dropped
}

func (b *Buffer) Pause() {
	if b.paused {
		return
	}
	b.paused = true
	b.snapshot = b.current()
	b.unread = 0
}

func (b *Buffer) Resume() {
	b.paused = false
	b.snapshot = nil
	b.unread = 0
}

func (b *Buffer) Paused() bool { return b.paused }

func (b *Buffer) Unread() int { return b.unread }

func (b *Buffer) Dropped() uint64 { return b.dropped }

func (b *Buffer) Len() int {
	if b.paused {
		return len(b.snapshot)
	}
	return b.size
}

func (b *Buffer) Visible() []Entry {
	if b.paused {
		return append([]Entry(nil), b.snapshot...)
	}
	return b.current()
}

func (b *Buffer) current() []Entry {
	result := make([]Entry, b.size)
	for index := range b.size {
		result[index] = b.entries[(b.start+index)%b.capacity]
	}
	return result
}
