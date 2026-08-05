package protocol

// TunStatus is the versioned status DTO for managed TUN mode.
type TunStatus struct {
	Schema        string `json:"schema"`
	Revision      uint64 `json:"revision"`
	DesiredEnable bool   `json:"desired_enable"`
	LiveEnable    *bool  `json:"live_enable,omitempty"`
	Stack         string `json:"stack,omitempty"`
	Managed       bool   `json:"managed"`
	LastError     string `json:"last_error,omitempty"`
}

// TunMutationRequest is the body for TUN enable/disable.
type TunMutationRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
}
