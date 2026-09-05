package protocol

import (
	"encoding/json"
	"testing"
)

func TestLoggingStatus_StableJSONContract(t *testing.T) {
	status := LoggingStatus{
		Schema:    "mihari/v1",
		Revision:  13,
		Level:     "info",
		MaxSizeMB: 10,
		MaxFiles:  3,
		Dir:       "/var/lib/mihari/logs",
	}

	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema":"mihari/v1","revision":13,"level":"info","max_size_mb":10,"max_files":3,"dir":"/var/lib/mihari/logs"}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}
}

func TestLoggingUpdateRequest_OptionalFieldsJSONContract(t *testing.T) {
	zero := uint64(0)
	level := "debug"
	maxSizeMB := int64(100)
	maxFiles := int64(10)

	request := LoggingUpdateRequest{
		OperationID: "logging-1",
		IfRevision:  &zero,
		Level:       &level,
		MaxSizeMB:   &maxSizeMB,
		MaxFiles:    &maxFiles,
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"operation_id":"logging-1","if_revision":0,"level":"debug","max_size_mb":100,"max_files":10}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}

	raw, err = json.Marshal(LoggingUpdateRequest{OperationID: "logging-2"})
	if err != nil {
		t.Fatal(err)
	}
	const wantOmitted = `{"operation_id":"logging-2"}`
	if string(raw) != wantOmitted {
		t.Fatalf("json=%s want=%s", raw, wantOmitted)
	}
}
