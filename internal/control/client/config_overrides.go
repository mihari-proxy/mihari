package client

import (
	"context"
	"net/http"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

// ConfigOverrides reads the instance's DNS form and ordered YAML fragments.
func (c *Client) ConfigOverrides(ctx context.Context) (protocol.ConfigOverridesStatus, error) {
	var result protocol.ConfigOverridesStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/config-overrides", nil, &result)
	return result, err
}

// UpdateConfigOverrides saves one editor's whole draft at the supplied revision.
func (c *Client) UpdateConfigOverrides(ctx context.Context, category string, req protocol.ConfigOverridesUpdateRequest) (protocol.ConfigOverridesStatus, error) {
	var result protocol.ConfigOverridesStatus
	if category != "dns" && category != "fragments" {
		return result, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "unknown override category"}
	}
	// Sending an empty collection explicitly clears the selected editor.
	if category == "dns" && req.DNS == nil {
		req.DNS = map[string]protocol.DNSFieldOverride{}
	}
	if category == "fragments" && req.Fragments == nil {
		req.Fragments = []protocol.OverrideFragment{}
	}
	err := c.doMutation(ctx, logging.OperationMetadata{ID: req.OperationID, Name: "config-overrides." + category}, http.MethodPut, "/v1/config-overrides/"+category, req, &result)
	return result, err
}

// PreviewConfigOverrides reads one page of effective YAML for the supplied draft.
func (c *Client) PreviewConfigOverrides(ctx context.Context, req protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error) {
	var result protocol.ConfigOverridesPreview
	err := c.doRuntime(ctx, http.MethodPost, "/v1/config-overrides/preview", req, &result)
	return result, err
}
