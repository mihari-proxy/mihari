package app

import (
	"io"
	"strconv"
)

const windowsRuntimeSchema = "mihari.windows-runtime/v1"

// windowsRuntimeRecord binds one protected Job generation to its daemon. An
// empty record still requires proof that the recorded daemon has exited.
type windowsRuntimeRecord struct {
	Schema           string `json:"schema"`
	InstallationID   string `json:"installation_id"`
	Generation       string `json:"generation"`
	BootID           string `json:"boot_id"`
	PID              uint32 `json:"pid"`
	CreationFiletime string `json:"creation_filetime"`
	BinarySHA256     string `json:"binary_sha256"`
	JobName          string `json:"job_name"`
	State            string `json:"state"`
}

func decodeWindowsRuntime(reader io.Reader) (windowsRuntimeRecord, error) {
	var record windowsRuntimeRecord
	keys, err := decodeInstallationJSON(reader, 4096, &record)
	if err != nil {
		return windowsRuntimeRecord{}, unknownInstallState()
	}
	for _, key := range []string{"schema", "installation_id", "generation", "boot_id", "pid", "creation_filetime", "binary_sha256", "job_name", "state"} {
		if !keys[key] {
			return windowsRuntimeRecord{}, unknownInstallState()
		}
	}
	creation, err := strconv.ParseUint(record.CreationFiletime, 10, 64)
	if record.Schema != windowsRuntimeSchema || !validTransactionID(record.InstallationID) || !validTransactionID(record.Generation) || !validTransactionID(record.BootID) || record.PID <= 1 || err != nil || creation == 0 || strconv.FormatUint(creation, 10) != record.CreationFiletime || !validSHA256(record.BinarySHA256) || record.JobName != `Global\Mihari.Runtime.`+record.Generation || record.State != "active" && record.State != "empty" {
		return windowsRuntimeRecord{}, unknownInstallState()
	}
	return record, nil
}
