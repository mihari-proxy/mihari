package mihomo

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// ProxyProvider describes one upstream source of proxy nodes.
type ProxyProvider struct {
	Name        string  `json:"name"`
	VehicleType string  `json:"vehicleType"`
	Proxies     []Proxy `json:"proxies"`
}

// ProxyProviders is the upstream provider discovery response.
type ProxyProviders struct {
	Providers map[string]ProxyProvider `json:"providers"`
}

// ProxyProviders reads provider nodes without modifying the active configuration.
func (c *Client) ProxyProviders(ctx context.Context) (ProxyProviders, error) {
	var result ProxyProviders
	err := c.do(ctx, http.MethodGet, "/providers/proxies", nil, nil, &result)
	if err == nil && result.Providers == nil {
		err = diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo returned invalid provider data"}, &diagnostics.HTTPError{Operation: "mihomo GET providers.proxies", Phase: "decode", Status: http.StatusOK, Cause: errors.New("provider response is missing the providers object")})
	}
	return result, err
}

// DelayProviderProxy tests one node within a named provider.
func (c *Client) DelayProviderProxy(ctx context.Context, provider, name, testURL string, timeout int) (uint16, error) {
	query := url.Values{"url": {testURL}, "timeout": {strconv.Itoa(timeout)}}
	var result struct {
		Delay uint16 `json:"delay"`
	}
	err := c.do(ctx, http.MethodGet, "/providers/proxies/"+url.PathEscape(provider)+"/"+url.PathEscape(name)+"/healthcheck", query, nil, &result)
	return result.Delay, err
}
