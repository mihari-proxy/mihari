package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatus_StartupNetworkJSONCompatibility(t *testing.T) {
	var status Status
	if err := json.Unmarshal([]byte(`{"schema":"mihari/v1","protocol_version":"v1","revision":7}`), &status); err != nil {
		t.Fatal(err)
	}
	if status.StartupNetwork.Applying() {
		t.Fatal("old daemon response inferred application")
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "startup_network") {
		t.Fatalf("idle field present: %s", raw)
	}
	status.StartupNetwork = &StartupNetworkStatus{TunApplying: true}
	raw, err = json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"startup_network":{"system_proxy_applying":false,"tun_applying":true}`) {
		t.Fatalf("wrong startup field: %s", raw)
	}
}
