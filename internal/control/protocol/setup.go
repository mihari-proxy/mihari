package protocol

const (
	// CapabilitySetupTarget advertises setup semantics for the available resource surfaces.
	CapabilitySetupTarget = "setup-target-v1"
	// ExecutionModeSetup applies an explicit setup target without a global revision prerequisite.
	ExecutionModeSetup = "setup"
)
