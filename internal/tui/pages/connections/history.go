package connections

import (
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type History struct {
	limit      int
	active     []protocol.Connection
	closed     []protocol.Connection
	observedAt time.Time
}

func NewHistory(limit int) *History {
	if limit <= 0 {
		limit = 500
	}
	return &History{limit: limit}
}

func (h *History) Observe(connections []protocol.Connection, observedAt time.Time) {
	previous := make(map[string]protocol.Connection, len(h.active))
	for _, connection := range h.active {
		previous[connection.ID] = connection
	}
	next := make([]protocol.Connection, 0, len(connections))
	elapsed := observedAt.Sub(h.observedAt).Seconds()
	for _, connection := range connections {
		connection = cloneConnection(connection)
		if prior, found := previous[connection.ID]; found && elapsed > 0 {
			connection.UploadSpeed = byteRate(connection.Upload-prior.Upload, elapsed)
			connection.DownloadSpeed = byteRate(connection.Download-prior.Download, elapsed)
		}
		next = append(next, connection)
		delete(previous, connection.ID)
	}
	for _, connection := range h.active {
		if _, missing := previous[connection.ID]; !missing {
			continue
		}
		connection = cloneConnection(connection)
		connection.ClosedAt = observedAt
		h.closed = append(h.closed, connection)
	}
	if len(h.closed) > h.limit {
		h.closed = append([]protocol.Connection(nil), h.closed[len(h.closed)-h.limit:]...)
	}
	h.active = next
	h.observedAt = observedAt
}

func (h *History) Active() []protocol.Connection { return cloneConnections(h.active) }

func (h *History) Closed() []protocol.Connection { return cloneConnections(h.closed) }

func (h *History) Reset() {
	h.active = nil
	h.closed = nil
	h.observedAt = time.Time{}
}

func byteRate(delta int64, seconds float64) int64 {
	if delta <= 0 || seconds <= 0 {
		return 0
	}
	return int64(float64(delta) / seconds)
}

func cloneConnections(connections []protocol.Connection) []protocol.Connection {
	result := make([]protocol.Connection, len(connections))
	for index, connection := range connections {
		result[index] = cloneConnection(connection)
	}
	return result
}

func cloneConnection(connection protocol.Connection) protocol.Connection {
	connection.Chains = append([]string(nil), connection.Chains...)
	return connection
}
