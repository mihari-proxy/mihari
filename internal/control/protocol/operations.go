package protocol

// OperationStatusCapability identifies process-local operation observation.
const OperationStatusCapability = "operation-status-v1"

// OperationStatus observes settlement. Finished does not imply success; unknown
// never proves that an operation did not execute.
type OperationStatus struct {
	Schema      string `json:"schema"`
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
}
