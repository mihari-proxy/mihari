package connections

import (
	"fmt"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestHistory_MissingActiveConnectionBecomesClosedAndCapsAt500(t *testing.T) {
	history := NewHistory(500)
	history.Observe(makeConnections("one", "two"), time.Unix(1, 0))
	history.Observe(makeConnections("two"), time.Unix(2, 0))
	if got := history.Closed()[0].ID; got != "one" {
		t.Fatalf("closed=%q", got)
	}
	for index := 0; index < 600; index++ {
		id := fmt.Sprintf("connection-%03d", index)
		history.Observe(makeConnections(id), time.Unix(int64(index*2+3), 0))
		history.Observe(nil, time.Unix(int64(index*2+4), 0))
	}
	closed := history.Closed()
	if len(closed) != 500 || closed[0].ID != "connection-100" || closed[499].ID != "connection-599" {
		t.Fatalf("closed len=%d first=%q last=%q", len(closed), closed[0].ID, closed[len(closed)-1].ID)
	}
}

func TestHistory_ComputesCurrentRatesWithoutChangingTotals(t *testing.T) {
	history := NewHistory(10)
	history.Observe([]protocol.Connection{{ID: "one", Upload: 100, Download: 200}}, time.Unix(1, 0))
	history.Observe([]protocol.Connection{{ID: "one", Upload: 300, Download: 500}}, time.Unix(3, 0))
	connection := history.Active()[0]
	if connection.UploadSpeed != 100 || connection.DownloadSpeed != 150 || connection.Upload != 300 || connection.Download != 500 {
		t.Fatalf("connection=%#v", connection)
	}
}

func makeConnections(ids ...string) []protocol.Connection {
	connections := make([]protocol.Connection, len(ids))
	for index, id := range ids {
		connections[index] = protocol.Connection{ID: id}
	}
	return connections
}
