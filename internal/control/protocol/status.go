package protocol

import "time"

type Status struct {
	Schema          string        `json:"schema"`
	ProtocolVersion string        `json:"protocol_version"`
	DaemonVersion   string        `json:"daemon_version"`
	Revision        uint64        `json:"revision"`
	Health          string        `json:"health"`
	StartedAt       time.Time     `json:"started_at"`
	Config          *ConfigStatus `json:"config,omitempty"`
}

type ConfigStatus struct {
	Status           string `json:"status"`
	DesiredRevision  uint64 `json:"desired_revision"`
	ObservedRevision uint64 `json:"observed_revision"`
	LastError        string `json:"last_error,omitempty"`
}
