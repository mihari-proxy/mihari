package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func testWindowsRuntimeRecord() windowsRuntimeRecord {
	generation := strings.Repeat("a", 32)
	return windowsRuntimeRecord{Schema: windowsRuntimeSchema, InstallationID: strings.Repeat("b", 32), Generation: generation, BootID: strings.Repeat("c", 32), PID: 123, CreationFiletime: "134000000000000000", BinarySHA256: strings.Repeat("d", 64), JobName: `Global\Mihari.Runtime.` + generation, State: "active"}
}

func TestWindowsRuntimeRecord_RequiresExactFieldsAndCanonicalIdentity(t *testing.T) {
	valid := testWindowsRuntimeRecord()
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"active", "empty"} {
		record := valid
		record.State = state
		body, _ := json.Marshal(record)
		got, err := decodeWindowsRuntime(bytes.NewReader(body))
		if err != nil || got != record {
			t.Fatalf("valid %s record rejected: %+v %v", state, got, err)
		}
	}
	mutations := map[string]func(*windowsRuntimeRecord){
		"schema":          func(r *windowsRuntimeRecord) { r.Schema = "other" },
		"id":              func(r *windowsRuntimeRecord) { r.InstallationID = "" },
		"generation case": func(r *windowsRuntimeRecord) { r.Generation = strings.Repeat("A", 32) },
		"boot":            func(r *windowsRuntimeRecord) { r.BootID = "" },
		"pid":             func(r *windowsRuntimeRecord) { r.PID = 1 },
		"time zero":       func(r *windowsRuntimeRecord) { r.CreationFiletime = "0" },
		"time padding":    func(r *windowsRuntimeRecord) { r.CreationFiletime = "01" },
		"time overflow":   func(r *windowsRuntimeRecord) { r.CreationFiletime = "18446744073709551616" },
		"binary":          func(r *windowsRuntimeRecord) { r.BinarySHA256 = "" },
		"job":             func(r *windowsRuntimeRecord) { r.JobName = `Local\Mihari.Runtime.` + r.Generation },
		"job generation":  func(r *windowsRuntimeRecord) { r.JobName = `Global\Mihari.Runtime.` + strings.Repeat("f", 32) },
		"state":           func(r *windowsRuntimeRecord) { r.State = "complete" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			record := valid
			mutate(&record)
			body, _ := json.Marshal(record)
			if _, err := decodeWindowsRuntime(bytes.NewReader(body)); err == nil {
				t.Fatal("invalid runtime identity accepted")
			}
		})
	}
	for _, key := range []string{"schema", "installation_id", "generation", "boot_id", "pid", "creation_filetime", "binary_sha256", "job_name", "state"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, key)
		body, _ := json.Marshal(fields)
		if _, err := decodeWindowsRuntime(bytes.NewReader(body)); err == nil {
			t.Errorf("missing %s accepted", key)
		}
	}
	for _, body := range []string{string(raw) + " {}", strings.Repeat(" ", 4096) + string(raw), strings.Replace(string(raw), `"pid":123`, `"pid":null`, 1), strings.Replace(string(raw), `"state":"active"`, `"state":"active","state":"empty"`, 1), strings.Replace(string(raw), `"pid":123`, `"PID":123`, 1)} {
		if _, err := decodeWindowsRuntime(strings.NewReader(body)); err == nil {
			t.Error("ambiguous runtime record accepted")
		}
	}
}
