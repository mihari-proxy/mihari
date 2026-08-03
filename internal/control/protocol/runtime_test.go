package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCoreStatusJSONDoesNotExposeControllerDetails(t *testing.T) {
	status := CoreStatus{
		Schema: "mihari/v1", Revision: 7, Status: "running", Version: "v1.19.0",
		PID: 42, Restarts: 2, NextRetryAt: time.Unix(100, 0).UTC(),
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"mihari/v1","revision":7,"status":"running","version":"v1.19.0","pid":42,"restarts":2,"next_retry_at":"1970-01-01T00:01:40Z"}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "controller") {
		t.Fatalf("sensitive field leaked: %s", raw)
	}
}

func TestRuntimeDTOsUseStableSchema(t *testing.T) {
	responses := []any{
		ProxyGroups{Schema: "mihari/v1", Groups: []ProxyGroup{{Name: "GLOBAL", Type: "Selector", Now: "DIRECT", All: []string{"DIRECT"}}}},
		ConnectionList{Schema: "mihari/v1", Connections: []Connection{{ID: "one"}}},
		RuleList{Schema: "mihari/v1", Rules: []Rule{{Type: "MATCH", Proxy: "DIRECT"}}},
		DelayResult{Schema: "mihari/v1", Delays: map[string]uint16{"DIRECT": 1}},
		MutationResult{Schema: "mihari/v1", OperationID: "op-1"},
	}
	for _, response := range responses {
		raw, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"schema":"mihari/v1"`) {
			t.Fatalf("response=%s", raw)
		}
	}
}
