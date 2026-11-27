package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

// A status can include the raw fragments from a 1 MiB settings file and a
// name from a 1 MiB catalog. JSON escapes individual characters to six bytes.
const overrideStatusResponseBytes = maxControlResponseSize + 6*(2<<20)

// ConfigOverrides reads the instance's DNS form and ordered YAML fragments.
func (c *Client) ConfigOverrides(ctx context.Context) (protocol.ConfigOverridesStatus, error) {
	var result protocol.ConfigOverridesStatus
	err := c.doRuntimeLimit(ctx, http.MethodGet, "/v1/config-overrides", nil, &result, overrideStatusResponseBytes)
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
	err := c.doMutation(ctx, logging.OperationMetadata{ID: req.OperationID, Name: "config-overrides." + category}, http.MethodPut, "/v1/config-overrides/"+category, req, &result, runtimeRequestOptions{responseLimit: overrideStatusResponseBytes})
	return result, err
}

// PreviewConfigOverrides reads one page of effective YAML for the supplied draft.
func (c *Client) PreviewConfigOverrides(ctx context.Context, req protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error) {
	var result protocol.ConfigOverridesPreview
	path := "/v1/config-overrides/preview"
	// Continuation metadata must not reduce the usable size of the draft body.
	if req.Offset != 0 || req.Snapshot != "" {
		cursor := url.Values{"offset": {strconv.Itoa(req.Offset)}, "snapshot": {req.Snapshot}}
		path += "?" + cursor.Encode()
		req.Offset, req.Snapshot = 0, ""
	}
	err := c.doRuntime(ctx, http.MethodPost, path, req, &result)
	return result, err
}
