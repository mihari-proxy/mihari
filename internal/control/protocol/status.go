package protocol

import "time"

const (
	CapabilityCore          = "core"
	CapabilityCoreReinstall = "core-reinstall"
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
	PID            int                   `json:"pid,omitempty"`
	StartupNetwork *StartupNetworkStatus `json:"startup_network,omitempty"`
}

// StartupNetworkStatus reports the daemon's initial network application only.
// It is transient observation, not evidence of drift repair or network health.
type StartupNetworkStatus struct {
	SystemProxyApplying bool `json:"system_proxy_applying"`
	TunApplying         bool `json:"tun_applying"`
}

// Applying reports whether either startup application is in progress.
func (s *StartupNetworkStatus) Applying() bool {
	return s != nil && (s.SystemProxyApplying || s.TunApplying)
}

type ConfigStatus struct {
	Status           string `json:"status"`
	DesiredRevision  uint64 `json:"desired_revision"`
	ObservedRevision uint64 `json:"observed_revision"`
	LastError        string `json:"last_error,omitempty"`
}
