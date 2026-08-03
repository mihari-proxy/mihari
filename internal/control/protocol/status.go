package protocol

import "time"

type Status struct {
	Schema          string    `json:"schema"`
	ProtocolVersion string    `json:"protocol_version"`
	DaemonVersion   string    `json:"daemon_version"`
	Revision        uint64    `json:"revision"`
	Health          string    `json:"health"`
	StartedAt       time.Time `json:"started_at"`
}
