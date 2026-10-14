package protocol

type OnboardingStatus struct {
	WarningOutcome
	Schema          string `json:"schema"`
	Revision        uint64 `json:"revision"`
	Complete        bool   `json:"complete"`
	MixedAddr       string `json:"mixed_addr"`
	ControllerAddr  string `json:"controller_addr"`
	WebAddr         string `json:"web_addr"`
	RestartRequired bool   `json:"restart_required"`
}

type OnboardingUpdateRequest struct {
	// ExecutionMode optionally selects daemon-owned setup target execution.
	ExecutionMode  string  `json:"execution_mode,omitempty"`
	OperationID    string  `json:"operation_id"`
	IfRevision     *uint64 `json:"if_revision,omitempty"`
	Complete       *bool   `json:"complete,omitempty"`
	MixedAddr      *string `json:"mixed_addr,omitempty"`
	ControllerAddr *string `json:"controller_addr,omitempty"`
	WebAddr        *string `json:"web_addr,omitempty"`
}
