package protocol

// DataResetResult is returned by POST /v1/data/reset after the daemon clears
// user data and returns the process to first-run setup.
type DataResetResult struct {
	Schema        string `json:"schema"`
	OperationID   string `json:"operation_id"`
	Revision      uint64 `json:"revision"`
	SetupRequired bool   `json:"setup_required"`
}
