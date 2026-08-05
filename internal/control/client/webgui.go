package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

// WebGUI returns the secret-free gateway and panel status.
func (c *Client) WebGUI(ctx context.Context) (protocol.WebGUIStatus, error) {
	var result protocol.WebGUIStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/web-gui", nil, &result)
	return result, err
}

// OpenWebGUI returns a short-lived open URL for the local browser helper.
// panelID selects an installed panel; empty opens the default active panel.
func (c *Client) OpenWebGUI(ctx context.Context, panelID string) (protocol.WebGUIOpenResult, error) {
	var result protocol.WebGUIOpenResult
	var body any
	if panelID != "" {
		body = protocol.WebGUIOpenRequest{Panel: panelID}
	}
	err := c.doRuntime(ctx, http.MethodPost, "/v1/web-gui/open", body, &result)
	return result, err
}

// Panels lists supported panels and install state.
func (c *Client) Panels(ctx context.Context) (protocol.PanelList, error) {
	var result protocol.PanelList
	err := c.doRuntime(ctx, http.MethodGet, "/v1/panels", nil, &result)
	return result, err
}

// InstallPanel downloads and installs a panel build.
func (c *Client) InstallPanel(ctx context.Context, id string, request protocol.PanelInstallRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/panels/"+url.PathEscape(id)+"/install", request, &result)
	return result, err
}

// UpdatePanel updates a panel to the latest resolved build.
func (c *Client) UpdatePanel(ctx context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/panels/"+url.PathEscape(id)+"/update", request, &result)
	return result, err
}

// ActivatePanel makes an installed panel the active Web GUI.
func (c *Client) ActivatePanel(ctx context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodPut, "/v1/panels/"+url.PathEscape(id)+"/active", request, &result)
	return result, err
}

// RollbackPanel restores the retained previous build for a panel.
func (c *Client) RollbackPanel(ctx context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/panels/"+url.PathEscape(id)+"/rollback", request, &result)
	return result, err
}

// UninstallPanel removes all local builds for a panel.
func (c *Client) UninstallPanel(ctx context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/panels/"+url.PathEscape(id)+"/uninstall", request, &result)
	return result, err
}

// ReinstallPanel uninstalls then reinstalls the latest build for a panel.
func (c *Client) ReinstallPanel(ctx context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/panels/"+url.PathEscape(id)+"/reinstall", request, &result)
	return result, err
}
