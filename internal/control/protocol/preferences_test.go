package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTUIPreferences_StableJSONContract(t *testing.T) {
	revision := uint64(7)
	request := UpdateTUIPreferencesRequest{
		OperationID:        "columns-1",
		IfRevision:         &revision,
		ConnectionsColumns: []string{"host", "chain", "traffic"},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"operation_id":"columns-1","if_revision":7,"connections_columns":["host","chain","traffic"]}`
	if string(raw) != wantJSON {
		t.Fatalf("json=%s want=%s", raw, wantJSON)
	}

	response := TUIPreferences{Schema: "mihari/v1", Revision: 8, ConnectionsColumns: request.ConnectionsColumns}
	raw, err = json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var got TUIPreferences
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "mihari/v1" || got.Revision != 8 || !reflect.DeepEqual(got.ConnectionsColumns, request.ConnectionsColumns) {
		t.Fatalf("got=%#v", got)
	}
}

func TestTUIPreferences_ExplicitEmptyListsSurviveEncodingForValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request UpdateTUIPreferencesRequest
		field   string
	}{
		{"columns", UpdateTUIPreferencesRequest{OperationID: "empty-columns", ConnectionsColumns: []string{}, LogLevels: []string{"warn"}}, `"connections_columns":[]`},
		{"levels", UpdateTUIPreferencesRequest{OperationID: "empty-levels", ConnectionsColumns: []string{"host"}, LogLevels: []string{}}, `"log_levels":[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.request)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), tc.field) {
				t.Fatalf("explicit empty list was silently omitted: %s", raw)
			}
		})
	}
}

func TestTUIPreferences_LatencyConcurrencyDefaultsAndContract(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{`{}`, 5},
		{`{"proxies":{"extra_latency":false,"auto_latency_test":false}}`, 5},
		{`{"proxies":{"latency_test_concurrency":1}}`, 1},
		{`{"proxies":{"latency_test_concurrency":50}}`, 50},
		{`{"proxies":{"latency_test_concurrency":-1}}`, 5},
		{`{"proxies":{"latency_test_concurrency":51}}`, 5},
	} {
		var prefs TUIPreferences
		if err := json.Unmarshal([]byte(tc.raw), &prefs); err != nil {
			t.Fatal(err)
		}
		if got := prefs.EffectiveProxies().LatencyTestConcurrency; got != tc.want {
			t.Fatalf("%s: concurrency=%d want=%d", tc.raw, got, tc.want)
		}
	}
	for _, limit := range []int{0, 5, 50} {
		raw, err := json.Marshal(UpdateTUIPreferencesRequest{OperationID: "fixture", Proxies: &ProxyPreferences{LatencyTestConcurrency: limit}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "latency_test_concurrency") != (limit != 0) {
			t.Fatalf("optional concurrency contract: %s", raw)
		}
		var request UpdateTUIPreferencesRequest
		if err := json.Unmarshal(raw, &request); err != nil || request.Proxies.LatencyTestConcurrency != limit {
			t.Fatalf("round-trip limit=%d err=%v", limit, err)
		}
	}
}
