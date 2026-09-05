package protocol

import "time"

const (
	CapabilityCore          = "core"
	CapabilityProxies       = "proxies"
	CapabilityConnections   = "connections"
	CapabilityRules         = "rules"
	CapabilityLogs          = "logs"
	CapabilityLogging       = "logging"
	CapabilitySubscriptions = "subscriptions"
	CapabilityRuleProviders = "rule-providers"
	CapabilityGeoIP         = "geoip"
	CapabilityPreferences   = "preferences"
	CapabilityOnboarding    = "onboarding"
	CapabilityWebGUI        = "web-gui"
	CapabilitySystemProxy   = "system-proxy"
	CapabilityTUN           = "tun"
)

type Status struct {
	Schema          string        `json:"schema"`
	ProtocolVersion string        `json:"protocol_version"`
	DaemonVersion   string        `json:"daemon_version"`
	Revision        uint64        `json:"revision"`
	Health          string        `json:"health"`
	LastError       string        `json:"last_error,omitempty"`
	StartedAt       time.Time     `json:"started_at"`
	Config          *ConfigStatus `json:"config,omitempty"`
	Capabilities    []string      `json:"capabilities,omitempty"`
	SetupRequired   bool          `json:"setup_required,omitempty"`
	// PID is this daemon process id. Optional additive field so local clients
	// can tell whether a TCP occupant is this instance's web gateway.
	PID int `json:"pid,omitempty"`
}

type ConfigStatus struct {
	Status           string `json:"status"`
	DesiredRevision  uint64 `json:"desired_revision"`
	ObservedRevision uint64 `json:"observed_revision"`
	LastError        string `json:"last_error,omitempty"`
}
