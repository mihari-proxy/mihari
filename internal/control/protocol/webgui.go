package protocol

// WebGUIStatus is the future-safe read model for the Mihari browser gateway.
type WebGUIStatus struct {
	Schema          string            `json:"schema"`
	Revision        uint64            `json:"revision"`
	GatewayAddr     string            `json:"gateway_addr"`
	GatewayHealth   string            `json:"gateway_health"`
	ActivePanel     string            `json:"active_panel,omitempty"`
	BrowserSessions int               `json:"browser_sessions"`
	Panels          []PanelStatus     `json:"panels"`
	Safeguards      GatewaySafeguards `json:"safeguards"`
}

// PanelStatus describes one supported Web GUI build without exposing download or authentication material.
type PanelStatus struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Active         bool   `json:"active"`
	InstalledBuild string `json:"installed_build,omitempty"`
	LatestBuild    string `json:"latest_build,omitempty"`
	Health         string `json:"health"`
	RollbackBuild  string `json:"rollback_build,omitempty"`
}

// GatewaySafeguards reports security invariants enforced by the future Web gateway.
type GatewaySafeguards struct {
	LoopbackBound        bool `json:"loopback_bound"`
	BrowserAuthenticated bool `json:"browser_authenticated"`
	ControllerIsolated   bool `json:"controller_isolated"`
	MutationsCoordinated bool `json:"mutations_coordinated"`
}
