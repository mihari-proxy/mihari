package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	maxControlResponseSize = 4 << 20
	maxControlStreamSize   = 1 << 20
)

func (c *Client) Core(ctx context.Context) (protocol.CoreStatus, error) {
	var result protocol.CoreStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/core", nil, &result)
	return result, err
}

func (c *Client) InstallCore(ctx context.Context, request protocol.MutationRequest) (protocol.CoreInstallResult, error) {
	var result protocol.CoreInstallResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/core/install", request, &result)
	return result, err
}

func (c *Client) RestartCore(ctx context.Context, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/core/restart", request, &result)
	return result, err
}

func (c *Client) ProxyGroups(ctx context.Context) (protocol.ProxyGroups, error) {
	var result protocol.ProxyGroups
	err := c.doRuntime(ctx, http.MethodGet, "/v1/proxies", nil, &result)
	return result, err
}

func (c *Client) SelectProxy(ctx context.Context, group string, request protocol.ProxySelectionRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodPut, "/v1/proxy-groups/"+url.PathEscape(group), request, &result)
	return result, err
}

func (c *Client) DelayTest(ctx context.Context, group string, request protocol.DelayTestRequest) (protocol.DelayResult, error) {
	var result protocol.DelayResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/proxy-groups/"+url.PathEscape(group)+"/delay-test", request, &result)
	return result, err
}

func (c *Client) DelayProxy(ctx context.Context, name string, request protocol.DelayTestRequest) (protocol.DelayResult, error) {
	var result protocol.DelayResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/proxies/"+url.PathEscape(name)+"/delay-test", request, &result)
	return result, err
}

func (c *Client) Connections(ctx context.Context) (protocol.ConnectionList, error) {
	var result protocol.ConnectionList
	err := c.doRuntime(ctx, http.MethodGet, "/v1/connections", nil, &result)
	return result, err
}

func (c *Client) CloseConnection(ctx context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodDelete, "/v1/connections/"+url.PathEscape(id), request, &result)
	return result, err
}

func (c *Client) CloseAllConnections(ctx context.Context, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodDelete, "/v1/connections", request, &result)
	return result, err
}

func (c *Client) Rules(ctx context.Context) (protocol.RuleList, error) {
	var result protocol.RuleList
	err := c.doRuntime(ctx, http.MethodGet, "/v1/rules", nil, &result)
	return result, err
}

func (c *Client) RuleProviders(ctx context.Context) (protocol.RuleProviderList, error) {
	var result protocol.RuleProviderList
	err := c.doRuntime(ctx, http.MethodGet, "/v1/rule-providers", nil, &result)
	return result, err
}

func (c *Client) UpdateRuleProvider(ctx context.Context, name string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/rule-providers/"+url.PathEscape(name)+"/update", request, &result)
	return result, err
}

// GeoIPStatus returns daemon-owned local database health.
func (c *Client) GeoIPStatus(ctx context.Context) (protocol.GeoIPStatus, error) {
	var result protocol.GeoIPStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/geoip/status", nil, &result)
	return result, err
}

// ServiceStatus returns the advisory OS service registration state for onboarding review.
func (c *Client) ServiceStatus(ctx context.Context) (protocol.ServiceStatus, error) {
	var result protocol.ServiceStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/service/status", nil, &result)
	return result, err
}

// LookupGeoIP resolves a bounded batch through the daemon.
func (c *Client) LookupGeoIP(ctx context.Context, request protocol.GeoIPLookupRequest) (protocol.GeoIPLookupResult, error) {
	var result protocol.GeoIPLookupResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/geoip/lookup", request, &result)
	return result, err
}

// UpdateGeoIP requests a coordinated Country/ASN database refresh.
func (c *Client) UpdateGeoIP(ctx context.Context, request protocol.MutationRequest) (protocol.GeoIPUpdateResult, error) {
	var result protocol.GeoIPUpdateResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/geoip/update", request, &result)
	return result, err
}

// SystemProxy returns desired intent and live OS system-proxy observation.
func (c *Client) SystemProxy(ctx context.Context) (protocol.SystemProxyStatus, error) {
	var result protocol.SystemProxyStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/system-proxy", nil, &result)
	return result, err
}

// EnableSystemProxy enables the OS system proxy via the daemon mutation path.
func (c *Client) EnableSystemProxy(ctx context.Context, request protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error) {
	var result protocol.SystemProxyStatus
	err := c.doRuntime(ctx, http.MethodPost, "/v1/system-proxy/enable", request, &result)
	return result, err
}

// DisableSystemProxy clears Mihari-owned system proxy via the daemon mutation path.
func (c *Client) DisableSystemProxy(ctx context.Context, request protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error) {
	var result protocol.SystemProxyStatus
	err := c.doRuntime(ctx, http.MethodPost, "/v1/system-proxy/disable", request, &result)
	return result, err
}

// Tun returns desired managed TUN intent and live mihomo observation when available.
func (c *Client) Tun(ctx context.Context) (protocol.TunStatus, error) {
	var result protocol.TunStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/tun", nil, &result)
	return result, err
}

// EnableTun enables managed TUN via the daemon mutation path.
func (c *Client) EnableTun(ctx context.Context, request protocol.TunMutationRequest) (protocol.TunStatus, error) {
	var result protocol.TunStatus
	err := c.doRuntime(ctx, http.MethodPost, "/v1/tun/enable", request, &result)
	return result, err
}

// DisableTun disables managed TUN via the daemon mutation path.
func (c *Client) DisableTun(ctx context.Context, request protocol.TunMutationRequest) (protocol.TunStatus, error) {
	var result protocol.TunStatus
	err := c.doRuntime(ctx, http.MethodPost, "/v1/tun/disable", request, &result)
	return result, err
}

func (c *Client) Onboarding(ctx context.Context) (protocol.OnboardingStatus, error) {
	var result protocol.OnboardingStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/onboarding", nil, &result)
	return result, err
}

func (c *Client) UpdateOnboarding(ctx context.Context, request protocol.OnboardingUpdateRequest) (protocol.OnboardingStatus, error) {
	var result protocol.OnboardingStatus
	err := c.doRuntime(ctx, http.MethodPatch, "/v1/onboarding", request, &result)
	return result, err
}

func (c *Client) TUIPreferences(ctx context.Context) (protocol.TUIPreferences, error) {
	var result protocol.TUIPreferences
	err := c.doRuntime(ctx, http.MethodGet, "/v1/preferences/tui", nil, &result)
	return result, err
}

func (c *Client) UpdateTUIPreferences(ctx context.Context, request protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error) {
	var result protocol.TUIPreferences
	err := c.doRuntime(ctx, http.MethodPatch, "/v1/preferences/tui", request, &result)
	return result, err
}

func (c *Client) Subscriptions(ctx context.Context) (protocol.SubscriptionList, error) {
	var result protocol.SubscriptionList
	err := c.doRuntime(ctx, http.MethodGet, "/v1/subscriptions", nil, &result)
	return result, err
}

func (c *Client) Subscription(ctx context.Context, id string) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doRuntime(ctx, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(id), nil, &result)
	return result, err
}

func (c *Client) AddSubscription(ctx context.Context, request protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/subscriptions", request, &result)
	return result, err
}

func (c *Client) RefreshSubscription(ctx context.Context, id string, request protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doRuntime(ctx, http.MethodPost, "/v1/subscriptions/"+url.PathEscape(id)+"/refresh", request, &result)
	return result, err
}

func (c *Client) UseSubscription(ctx context.Context, id string, request protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doRuntime(ctx, http.MethodPut, "/v1/subscriptions/"+url.PathEscape(id)+"/active", request, &result)
	return result, err
}

func (c *Client) SetSubscriptionEnabled(ctx context.Context, id string, request protocol.SubscriptionEnabledRequest) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doRuntime(ctx, http.MethodPut, "/v1/subscriptions/"+url.PathEscape(id)+"/enabled", request, &result)
	return result, err
}

func (c *Client) UpdateSubscription(ctx context.Context, id string, request protocol.SubscriptionUpdateRequest) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doRuntime(ctx, http.MethodPatch, "/v1/subscriptions/"+url.PathEscape(id), request, &result)
	return result, err
}

func (c *Client) RemoveSubscription(ctx context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doRuntime(ctx, http.MethodDelete, "/v1/subscriptions/"+url.PathEscape(id), request, &result)
	return result, err
}

func (c *Client) Stream(ctx context.Context, kind string, receive func(protocol.StreamEvent) error) error {
	if receive == nil {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "stream receiver is required"}
	}
	streamURL, err := url.Parse(c.baseURL)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeInternal, Message: "invalid local control address"}
	}
	switch streamURL.Scheme {
	case "http":
		streamURL.Scheme = "ws"
	case "https":
		streamURL.Scheme = "wss"
	default:
		return protocol.APIError{Code: protocol.CodeInternal, Message: "invalid local control address"}
	}
	streamURL.Path = strings.TrimRight(streamURL.Path, "/") + "/v1/streams/" + url.PathEscape(kind)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.bearerToken())
	connection, response, err := websocket.Dial(ctx, streamURL.String(), &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: header})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if response != nil {
			return decodeRuntimeHTTPError(response)
		}
		return protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: "daemon is unavailable"}
	}
	defer connection.CloseNow()
	connection.SetReadLimit(maxControlStreamSize)
	for {
		_, raw, err := connection.Read(ctx)
		if err != nil {
			if ctx.Err() != nil || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil
			}
			if errors.Is(err, websocket.ErrMessageTooBig) {
				return protocol.APIError{Code: protocol.CodeDataFailure, Message: "control stream message is too large"}
			}
			return protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: "control stream closed unexpectedly"}
		}
		var event protocol.StreamEvent
		if err := json.Unmarshal(raw, &event); err != nil || event.Schema != "mihari/v1" || event.Stream != kind {
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid control stream event"}
		}
		if err := receive(event); err != nil {
			return err
		}
	}
}

func (c *Client) doRuntime(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return protocol.APIError{Code: protocol.CodeInternal, Message: "encode control request"}
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeInternal, Message: "create control request"}
	}
	request.Header.Set("Authorization", "Bearer "+c.bearerToken())
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: "daemon is unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeRuntimeHTTPError(response)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxControlResponseSize+1))
	if err != nil {
		return protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: "read control response failed"}
	}
	if len(raw) > maxControlResponseSize {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "control response is too large"}
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid control response"}
	}
	return nil
}

func decodeRuntimeHTTPError(response *http.Response) error {
	defer response.Body.Close()
	var envelope protocol.ErrorEnvelope
	if err := json.NewDecoder(io.LimitReader(response.Body, maxControlResponseSize)).Decode(&envelope); err != nil || envelope.Error.Code == "" {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid control error response"}
	}
	return envelope.Error
}
