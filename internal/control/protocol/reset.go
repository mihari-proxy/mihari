package protocol

// CapabilityDataReset identifies the daemon-owned, resource-preserving reset.
const CapabilityDataReset = "business-reset-v1"

// ResetPreview names exact managed files and a digest bound to their content.
type ResetPreview struct {
	Schema    string   `json:"schema"`
	Revision  uint64   `json:"revision"`
	Digest    string   `json:"digest"`
	Targets   []string `json:"targets"`
	Preserved []string `json:"preserved"`
}

// DataResetRequest confirms a previously reviewed reset plan.
type DataResetRequest struct {
	OperationID  string  `json:"operation_id"`
	IfRevision   *uint64 `json:"if_revision,omitempty"`
	Digest       string  `json:"digest"`
	Confirm      bool    `json:"confirm"`
	ConfirmAgain bool    `json:"confirm_again"`
}

// DataResetResult directs clients to Setup after a confirmed reset.
type DataResetResult struct {
	WarningOutcome
	Schema          string `json:"schema"`
	Revision        uint64 `json:"revision"`
	SetupRequired   bool   `json:"setup_required"`
	RestartRequired bool   `json:"restart_required"`
}
