package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStatus_AdditiveCapabilitiesRoundTrip(t *testing.T) {
	want := Status{
		Schema:        "mihari/v1",
		Capabilities:  []string{CapabilityCore, CapabilityLogs},
		SetupRequired: true,
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Status
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Capabilities, want.Capabilities) || !got.SetupRequired {
		t.Fatalf("got=%#v", got)
	}
}

func TestStatus_OldJSONStillDecodes(t *testing.T) {
	var got Status
	if err := json.Unmarshal([]byte(`{"schema":"mihari/v1","protocol_version":"v1"}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "mihari/v1" || got.ProtocolVersion != "v1" || len(got.Capabilities) != 0 || got.SetupRequired {
		t.Fatalf("got=%#v", got)
	}
}

func TestTypedStreamPayloadsUseMihomoFieldNames(t *testing.T) {
	tests := []struct {
		value any
		want  string
	}{
		{TrafficSample{Up: 1, Down: 2}, `{"up":1,"down":2}`},
		{MemorySample{InUse: 3, OSLimit: 4}, `{"inuse":3,"oslimit":4}`},
		{LogEntry{Level: "info", Message: "ready"}, `{"type":"info","payload":"ready"}`},
	}
	for _, test := range tests {
		raw, err := json.Marshal(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != test.want {
			t.Fatalf("json=%s want=%s", raw, test.want)
		}
	}
}

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
		RuleProviderList{Schema: "mihari/v1", Providers: []RuleProvider{{Name: "OpenAI", Type: "HTTP", Behavior: "Classical", Format: "YamlRule", RuleCount: 12, Status: "Ready"}}},
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

func TestRuleProviderListJSONContract(t *testing.T) {
	result := RuleProviderList{
		Schema: "mihari/v1", Revision: 9,
		Providers: []RuleProvider{{
			Name: "OpenAI", Type: "HTTP", Behavior: "Classical", Format: "YamlRule",
			RuleCount: 12, UpdatedAt: time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC), Status: "Ready",
		}},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"mihari/v1","revision":9,"providers":[{"name":"OpenAI","type":"HTTP","behavior":"Classical","format":"YamlRule","rule_count":12,"updated_at":"2026-08-03T01:02:03Z","status":"Ready"}]}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}
}
