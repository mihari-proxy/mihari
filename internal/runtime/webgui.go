package runtime

import (
	"context"
	"net"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/panel"
)

// WebGUIStatus returns a secret-free gateway and panel status for local clients.
func (m *Manager) WebGUIStatus(context.Context) (protocol.WebGUIStatus, error) {
	if m.webGateway == nil || m.panels == nil {
		return protocol.WebGUIStatus{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "web gui is unavailable"}
	}
	revision := m.store.Load().Revision
	addr := m.WebListenAddr()
	active, err := m.panels.Active()
	if err != nil {
		return protocol.WebGUIStatus{}, err
	}
	panels := m.panels.List()
	statusPanels := make([]protocol.PanelStatus, 0, len(panels))
	for _, info := range panels {
		statusPanels = append(statusPanels, panelInfoDTO(info))
	}
	health := "healthy"
	if active.Panel == "" {
		health = "idle"
	}
	return protocol.WebGUIStatus{
		Schema: "mihari/v1", Revision: revision, GatewayAddr: addr, GatewayHealth: health,
		ActivePanel: active.Panel, BrowserSessions: m.BrowserSessions(), Panels: statusPanels,
		Safeguards: protocol.GatewaySafeguards{
			LoopbackBound:        isLoopbackAddr(addr),
			BrowserAuthenticated: true,
			ControllerIsolated:   true,
			MutationsCoordinated: true,
		},
	}, nil
}

// ListPanels returns redacted panel install state.
func (m *Manager) ListPanels(context.Context) ([]panel.PanelInfo, error) {
	if m.panels == nil {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
	}
	return m.panels.List(), nil
}

// OpenWebGUI returns a one-shot local open URL that includes the Web access token.
// panelID selects which installed panel to open; empty uses the default active panel.
// Callers must not log or persist the result beyond immediately launching a browser.
func (m *Manager) OpenWebGUI(_ context.Context, panelID string) (string, string, error) {
	if m.webGateway == nil || m.webOpenToken == "" {
		return "", "", protocol.APIError{Code: protocol.CodeInvalidState, Message: "web gui open is unavailable"}
	}
	id := strings.TrimSpace(panelID)
	if id == "" {
		if m.panels != nil {
			active, err := m.panels.Active()
			if err != nil {
				return "", "", err
			}
			id = active.Panel
		}
	}
	if id != "" {
		if m.panels == nil {
			return "", "", protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel service is unavailable"}
		}
		dir, err := m.panels.PanelDir(id)
		if err != nil {
			return "", "", err
		}
		if dir == "" {
			return "", "", protocol.APIError{Code: protocol.CodeInvalidState, Message: "panel is not installed"}
		}
	}
	addr := m.WebListenAddr()
	if addr == "" {
		addr = m.settings.WebAddr
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	base := strings.TrimRight(addr, "/")
	if id == "" {
		return base + "/?token=" + m.webOpenToken, "", nil
	}
	return base + panel.UIMount(id) + "/?token=" + m.webOpenToken, id, nil
}

func panelInfoDTO(info panel.PanelInfo) protocol.PanelStatus {
	return protocol.PanelStatus{
		ID: info.ID, Name: info.Name, Active: info.Active,
		InstalledBuild: info.InstalledBuild, LatestBuild: info.LatestBuild,
		Health: info.Health, RollbackBuild: info.RollbackBuild,
	}
}

func isLoopbackAddr(addr string) bool {
	host := addr
	if strings.Contains(addr, "://") {
		host = strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}
