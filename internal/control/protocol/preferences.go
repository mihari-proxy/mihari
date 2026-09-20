package protocol

type TUIPreferences struct {
	WarningOutcome
	Schema             string            `json:"schema"`
	Revision           uint64            `json:"revision"`
	ConnectionsColumns []string          `json:"connections_columns"`
	Proxies            *ProxyPreferences `json:"proxies,omitempty"`
	// LogLevels contains the daemon's saved display selection, not its logging threshold.
	LogLevels []string `json:"log_levels,omitempty"`
}

// ProxyPreferences controls page presentation and automatic latency tests.
type ProxyPreferences struct {
	ExtraLatency    bool `json:"extra_latency"`
	AutoLatencyTest bool `json:"auto_latency_test"`
}

// EffectiveProxies supplies the defaults when an older server omits the block.
func (p TUIPreferences) EffectiveProxies() ProxyPreferences {
	if p.Proxies == nil {
		return ProxyPreferences{ExtraLatency: true, AutoLatencyTest: true}
	}
	return *p.Proxies
}

type UpdateTUIPreferencesRequest struct {
	OperationID        string            `json:"operation_id"`
	IfRevision         *uint64           `json:"if_revision,omitempty"`
	ConnectionsColumns []string          `json:"connections_columns"`
	Proxies            *ProxyPreferences `json:"proxies,omitempty"`
	// LogLevels preserves the saved selection when nil; an explicit empty list is invalid.
	LogLevels []string `json:"log_levels,omitzero"`
}
