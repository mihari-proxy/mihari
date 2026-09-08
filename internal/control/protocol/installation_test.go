package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

const validInstallationStatusJSON = `{"schema":"mihari.install-status/v1","kind":"interrupted","service_state":"stopped","start_failed":false,"reason":"operation_interrupted","id":"0123456789abcdef0123456789abcdef"}`

func TestInstallationStatus_StrictDecode(t *testing.T) {
	for _, raw := range []string{
		`{}`, strings.Replace(validInstallationStatusJSON, `"start_failed":false`, `"start_failed":null`, 1),
		strings.Replace(validInstallationStatusJSON, `"kind":"interrupted"`, `"kind":"interrupted","kind":"installed"`, 1),
		strings.Replace(validInstallationStatusJSON, `"kind"`, `"Kind"`, 1),
		strings.Replace(validInstallationStatusJSON, `false`, `true`, 1),
		strings.Replace(validInstallationStatusJSON, `"stopped"`, `"broken"`, 1),
		strings.Replace(validInstallationStatusJSON, `"id":`, `"private_path":"secret","id":`, 1),
		strings.Replace(validInstallationStatusJSON, "operation_interrupted", strings.Repeat("x", 4096), 1),
	} {
		var status InstallationStatus
		if err := json.Unmarshal([]byte(raw), &status); err == nil {
			t.Fatal("accepted ambiguous status")
		}
	}
}

func TestInstallationStatus_ValidAndFutureReason(t *testing.T) {
	for _, raw := range []string{validInstallationStatusJSON, strings.Replace(validInstallationStatusJSON, "operation_interrupted", "future_reason", 1)} {
		var status InstallationStatus
		if err := json.Unmarshal([]byte(raw), &status); err != nil {
			t.Fatal(err)
		}
		if status.Kind != "interrupted" || status.ServiceState != "stopped" {
			t.Fatal("lost installation observation")
		}
		if err := status.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
