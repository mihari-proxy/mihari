package protocol

// CapabilityRouting identifies daemon-owned routing mode and GLOBAL selection.
const CapabilityRouting = "routing-mode-v1"

// ValidRoutingMode reports whether mode is a supported mihomo routing strategy.
func ValidRoutingMode(mode string) bool {
	return mode == "rule" || mode == "global" || mode == "direct"
}

// RoutingStatus separates persisted intent from confirmed kernel observation.
type RoutingStatus struct {
	WarningOutcome
	Schema              string `json:"schema"`
	Revision            uint64 `json:"revision"`
	DesiredMode         string `json:"desired_mode"`
	LiveMode            string `json:"live_mode,omitempty"`
	State               string `json:"state"`
	SubscriptionID      string `json:"subscription_id,omitempty"`
	GlobalSelection     string `json:"global_selection,omitempty"`
	LiveGlobalSelection string `json:"live_global_selection,omitempty"`
	Message             string `json:"message,omitempty"`
}

// RoutingUpdateRequest changes the daemon-owned mode without closing connections.
type RoutingUpdateRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
	Mode        string  `json:"mode"`
}
