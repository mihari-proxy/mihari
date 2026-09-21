package protocol

// ApplicationUpdateCapability advertises authenticated Windows update preparation.
const ApplicationUpdateCapability = "app-update-prepare-v1"

// UpdateFreshConnectionMessage identifies the bounded retry needed for a newly started pipe client.
const UpdateFreshConnectionMessage = "update caller identity requires a fresh connection"

// ApplicationUpdatePrepared is identity evidence for a drained daemon, not a remote kill command.
type ApplicationUpdatePrepared struct {
	RuntimeJob       string `json:"runtime_job"`
	Schema           string `json:"schema"`
	OperationID      string `json:"operation_id"`
	PID              uint32 `json:"pid"`
	CreationFiletime uint64 `json:"creation_filetime"`
	ImagePath        string `json:"image_path"`
	SID              string `json:"sid"`
}
