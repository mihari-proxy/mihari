package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/panel"
)

func TestWebGUIStatusAndOpenURL(t *testing.T) {
	panels := &fakePanels{
		list:   []panel.PanelInfo{{ID: panel.IDZashboard, Name: "Zashboard", Active: true, InstalledBuild: "v1", Health: "active"}},
		active: panel.Active{Panel: panel.IDZashboard, Build: "v1"},
	}
	settings := config.Defaults()
	settings.WebAddr = "127.0.0.1:9191"
	manager := newTestManager(Options{
		Panels: panels, WebGateway: stubGateway{}, WebOpenToken: "web-open-token-value", Settings: settings,
	})
	status, err := manager.WebGUIStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ActivePanel != panel.IDZashboard || status.GatewayAddr == "" || !status.Safeguards.ControllerIsolated {
		t.Fatalf("status=%#v", status)
	}
	if strings.Contains(strings.ToLower(status.GatewayAddr+status.ActivePanel), "token") {
		t.Fatal("status embedded token material")
	}
	openURL, err := manager.OpenWebGUI(context.Background())
	if err != nil || !strings.Contains(openURL, "token=web-open-token-value") {
		t.Fatalf("openURL=%q err=%v", openURL, err)
	}
}
