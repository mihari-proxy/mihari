package session

import (
	"encoding/json"
	"fmt"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func decodeStreamEvent(source protocol.StreamEvent) (Event, error) {
	event := Event{ObservedAt: source.ObservedAt}
	var target any
	switch source.Stream {
	case "traffic":
		event.Kind = EventTraffic
		target = &event.Traffic
	case "memory":
		event.Kind = EventMemory
		target = &event.Memory
	case "logs":
		event.Kind = EventLog
		target = &event.Log
	case "connections":
		event.Kind = EventConnections
		target = &event.Connections
	default:
		return Event{}, fmt.Errorf("decode control stream: unsupported stream %q", source.Stream)
	}
	if err := json.Unmarshal(source.Data, target); err != nil {
		return Event{}, fmt.Errorf("decode %s stream payload: %w", source.Stream, err)
	}
	return event, nil
}
