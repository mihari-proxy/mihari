package protocol

// TunStatus is the versioned status DTO for managed TUN mode.
type TunStatus struct {
	Schema        string       `json:"schema"`
	Revision      uint64       `json:"revision"`
	DesiredEnable bool         `json:"desired_enable"`
	LiveEnable    *bool        `json:"live_enable,omitempty"`
	Stack         string       `json:"stack,omitempty"`
	Managed       bool         `json:"managed"`
	LastError     string       `json:"last_error,omitempty"`
	Conflict      *TunConflict `json:"conflict,omitempty"`
}

// TunConflict carries structured evidence of other TUN-capable actors on the
// system, split by signal type so callers can present distinct guidance.
// A nil Conflict means no evidence was detected.
//
// Unlike system proxy, TUN has no ownership concept: a TUN adapter carries no
// owner tag, and disabling this daemon's own mihomo tun block is non-destructive
// to other actors. Therefore only enable is gated (CodeTunConflict); disable is
// never gated. This asymmetry is intentional, not an omission.
type TunConflict struct {
	// OtherTunInterfaces lists other TUN adapters detected on the system
	// (signal A: wintun/tun/utun), with this daemon's own adapter subtracted.
	// A non-empty slice is the gate condition that makes enable return
	// CodeTunConflict (absent --force / confirmation).
	OtherTunInterfaces []string `json:"other_tun_interfaces,omitempty"`
	// OtherMihomoProcesses lists other mihomo processes detected on the
	// system (signal B), with this instance subtracted. Corroborating
	// evidence only; it never gates enable on its own.
	OtherMihomoProcesses []string `json:"other_mihomo_processes,omitempty"`
}

// TunMutationRequest is the body for TUN enable/disable.
type TunMutationRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
	// Force overrides the TUN conflict gate when other TUN adapters are
	// detected. Disable is never gated, so Force only affects enable.
	Force bool `json:"force,omitempty"`
}
