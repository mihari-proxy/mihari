package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func TestDecodeStreamEventReturnsTypedPayload(t *testing.T) {
	observed := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	event, err := decodeStreamEvent(protocol.StreamEvent{
		Schema: "mihari/v1", Stream: "traffic", ObservedAt: observed,
		Data: json.RawMessage(`{"up":12,"down":34}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != EventTraffic || event.ObservedAt != observed || event.Traffic.Up != 12 || event.Traffic.Down != 34 {
		t.Fatalf("event=%#v", event)
	}
}

func TestDecodeStreamEventRejectsInvalidPayload(t *testing.T) {
	for _, event := range []protocol.StreamEvent{
		{Stream: "unknown", Data: json.RawMessage(`{}`)},
		{Stream: "logs", Data: json.RawMessage(`{"type":`)},
	} {
		if _, err := decodeStreamEvent(event); err == nil {
			t.Fatalf("event=%#v", event)
		}
	}
}
