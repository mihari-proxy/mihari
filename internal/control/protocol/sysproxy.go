package protocol

// SystemProxyStatus is the versioned status DTO for OS system proxy.
type SystemProxyStatus struct {
	Schema    string              `json:"schema"`
	Revision  uint64              `json:"revision"`
	Desired   bool                `json:"desired"`
	Target    string              `json:"target,omitempty"`
	Observed  SystemProxyObserved `json:"observed"`
	LastError string              `json:"last_error,omitempty"`
}

// SystemProxyObserved reports the live OS proxy state as seen by the daemon.
type SystemProxyObserved struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server,omitempty"`
	Owned   bool   `json:"owned"`
	Foreign bool   `json:"foreign"`
}

// SystemProxyMutationRequest is the body for system-proxy enable/disable.
type SystemProxyMutationRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
	Force       bool    `json:"force,omitempty"`
}
