package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/panel"
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
	openURL, panelID, err := manager.OpenWebGUI(context.Background(), "")
	if err != nil || !strings.Contains(openURL, "token=web-open-token-value") {
		t.Fatalf("openURL=%q err=%v", openURL, err)
	}
	if panelID != panel.IDZashboard || !strings.Contains(openURL, "/__mihari/panels/zashboard/") {
		t.Fatalf("default open should target active panel mount, openURL=%q panel=%q", openURL, panelID)
	}
	openURL, panelID, err = manager.OpenWebGUI(context.Background(), panel.IDMetaCubeXD)
	if err != nil || panelID != panel.IDMetaCubeXD || !strings.Contains(openURL, "/__mihari/panels/metacubexd/") {
		t.Fatalf("openURL=%q panel=%q err=%v", openURL, panelID, err)
	}
	if strings.Contains(openURL, "zashboard") {
		t.Fatalf("selected panel open leaked default panel: %s", openURL)
	}
}

type unlockedActivePanels struct {
	fakePanels
	manager *Manager
}

func (p *unlockedActivePanels) Active() (panel.Active, error) {
	if !p.manager.settingsMu.TryLock() {
		return panel.Active{}, errors.New("settings lock held across panel IO")
	}
	p.manager.settingsMu.Unlock()
	return panel.Active{}, nil
}
func TestOpenWebGUI_ReleasesSettingsLockBeforePanelAccess(t *testing.T) {
	panels := &unlockedActivePanels{}
	m := newTestManager(Options{Panels: panels, WebGateway: stubGateway{}, WebOpenToken: "fixture"})
	panels.manager = m
	if _, _, err := m.OpenWebGUI(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
}
