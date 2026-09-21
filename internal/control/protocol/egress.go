package protocol

// CapabilityEgress identifies daemon-owned outbound interface selection.
const CapabilityEgress = "egress-interface-v1"

// EgressInterface describes a local interface, including unavailable saved names.
type EgressInterface struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Availability string   `json:"availability"`
	Addresses    []string `json:"addresses"`
	Selectable   bool     `json:"selectable"`
	Reason       string   `json:"reason,omitempty"`
}

// EgressSelection is automatic or bound to an exact local interface name.
type EgressSelection struct {
	Mode          string `json:"mode"`
	InterfaceName string `json:"interface_name,omitempty"`
}

// EgressStatus separates saved configuration from observed application state.
type EgressStatus struct {
	WarningOutcome
	Schema     string            `json:"schema"`
	Revision   uint64            `json:"revision"`
	Selection  EgressSelection   `json:"selection"`
	State      string            `json:"state"`
	Interfaces []EgressInterface `json:"interfaces"`
}

// EgressUpdateRequest changes the instance-wide outbound interface.
type EgressUpdateRequest struct {
	OperationID   string  `json:"operation_id"`
	IfRevision    *uint64 `json:"if_revision,omitempty"`
	Mode          string  `json:"mode"`
	InterfaceName string  `json:"interface_name,omitempty"`
}

// ValidEgressSelection rejects ambiguous automatic/manual requests.
func ValidEgressSelection(mode, name string) bool {
	return mode == "automatic" && name == "" || mode == "manual" && name != ""
}
