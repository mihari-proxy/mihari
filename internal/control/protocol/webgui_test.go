package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWebGUIStatusUsesFutureSafeSecretFreeJSON(t *testing.T) {
	status := WebGUIStatus{
		Schema: "mihari/v1", Revision: 4, GatewayAddr: "127.0.0.1:9191", GatewayHealth: "healthy",
		ActivePanel: "zashboard", BrowserSessions: 2,
		Panels: []PanelStatus{
			{ID: "zashboard", Name: "Zashboard", Active: true, InstalledBuild: "v2.1.0", LatestBuild: "v2.2.0", Health: "healthy", RollbackBuild: "v2.0.0"},
			{ID: "metacubexd", Name: "MetaCubeXD", InstalledBuild: "8e31c4a", LatestBuild: "c12ad90"},
		},
		Safeguards: GatewaySafeguards{LoopbackBound: true, BrowserAuthenticated: true, ControllerIsolated: true, MutationsCoordinated: true},
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret", "token", "controller_addr", "authentication_url", "open_url", "credential"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Fatalf("web GUI JSON leaked %q: %s", forbidden, raw)
		}
	}
	var decoded WebGUIStatus
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Panels[1].InstalledBuild != "8e31c4a" || !decoded.Safeguards.MutationsCoordinated {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
}

func TestPanelListAndInstallRequestJSONContract(t *testing.T) {
	list := PanelList{Schema: "mihari/v1", Revision: 2, Panels: []PanelStatus{{ID: "zashboard", Name: "Zashboard", Health: "missing"}}}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "secret") {
		t.Fatalf("panel list leaked secret: %s", raw)
	}
	req := PanelInstallRequest{OperationID: "op-1", Build: "v1.0.0"}
	raw, err = json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PanelInstallRequest
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.OperationID != "op-1" || decoded.Build != "v1.0.0" {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
}
