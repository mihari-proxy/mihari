package protocol

// WebGUIStatus is the future-safe read model for the Mihari browser gateway.
// It must never include controller secrets, web credentials, or open-browser tokens.
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

// PanelList is the additive list response for GET /v1/panels.
type PanelList struct {
	Schema   string        `json:"schema"`
	Revision uint64        `json:"revision"`
	Panels   []PanelStatus `json:"panels"`
}

// PanelInstallRequest installs or pins a panel build.
type PanelInstallRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
	Build       string  `json:"build,omitempty"`
}

// WebGUIOpenRequest selects which installed panel to open. Empty panel opens the default active panel.
type WebGUIOpenRequest struct {
	Panel string `json:"panel,omitempty"`
}

// WebGUIOpenResult returns a short-lived open URL only to privileged local clients.
// Consumers must open immediately and must not store the URL in snapshots or logs.
// OpenURL points at /__mihari/panels/{panel}/ when a panel is installed so multiple panels can run concurrently.
type WebGUIOpenResult struct {
	Schema  string `json:"schema"`
	OpenURL string `json:"open_url"`
	Panel   string `json:"panel,omitempty"`
}

// GatewaySafeguards reports security invariants enforced by the Web gateway.
type GatewaySafeguards struct {
	LoopbackBound        bool `json:"loopback_bound"`
	BrowserAuthenticated bool `json:"browser_authenticated"`
	ControllerIsolated   bool `json:"controller_isolated"`
	MutationsCoordinated bool `json:"mutations_coordinated"`
}
