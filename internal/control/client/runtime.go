package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

const (
	maxControlResponseSize = 4 << 20
	maxControlStreamSize   = 1 << 20
)

type runtimeOutcome struct {
	err            error
	remoteEnvelope bool
}

func (c *Client) Core(ctx context.Context) (protocol.CoreStatus, error) {
	var result protocol.CoreStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/core", nil, &result)
	return result, err
}

func (c *Client) InstallCore(ctx context.Context, request protocol.MutationRequest) (protocol.CoreInstallResult, error) {
	var result protocol.CoreInstallResult
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "core.install"}, http.MethodPost, "/v1/core/install", request, &result)
	return result, err
}

func (c *Client) RestartCore(ctx context.Context, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "core.restart"}, http.MethodPost, "/v1/core/restart", request, &result)
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
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "rule_provider.refresh"}, http.MethodPost, "/v1/rule-providers/"+url.PathEscape(name)+"/update", request, &result)
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
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "geoip.update"}, http.MethodPost, "/v1/geoip/update", request, &result)
	return result, err
}

// Logging returns the daemon-owned effective file logging configuration.
func (c *Client) Logging(ctx context.Context) (protocol.LoggingStatus, error) {
	var result protocol.LoggingStatus
	err := c.doRuntime(ctx, http.MethodGet, "/v1/logging", nil, &result)
	return result, err
}

// UpdateLogging applies a partial daemon-owned file logging configuration update.
func (c *Client) UpdateLogging(ctx context.Context, request protocol.LoggingUpdateRequest) (protocol.LoggingStatus, error) {
	ctx = logging.WithOperation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "logging.update"})
	var result protocol.LoggingStatus
	reporter := c.diagnosticReporter()
	if reporter != nil {
		reporter(ctx, diagnostics.Record{Component: "control.client", Event: "logging_update_started", Level: slog.LevelDebug})
	}
	outcome := c.doRuntimeOutcome(ctx, http.MethodPatch, "/v1/logging", request, &result, maxControlResponseSize)
	if reporter != nil {
		switch {
		case outcome.err == nil:
			reporter(ctx, diagnostics.Record{Component: "control.client", Event: "logging_update_succeeded", Level: slog.LevelDebug})
		case outcome.remoteEnvelope:
			reporter(ctx, diagnostics.Record{Component: "control.client", Event: "logging_update_response", Level: slog.LevelDebug, Err: outcome.err})
		default:
			if level, report := diagnostics.FailureLevel(ctx, outcome.err); report {
				reporter(ctx, diagnostics.Record{Component: "control.client", Event: "logging_update_failed", Level: level, Err: outcome.err})
			}
		}
	}
	return result, outcome.err
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
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "system_proxy.enable"}, http.MethodPost, "/v1/system-proxy/enable", request, &result)
	return result, err
}

// DisableSystemProxy clears Mihari-owned system proxy via the daemon mutation path.
func (c *Client) DisableSystemProxy(ctx context.Context, request protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error) {
	var result protocol.SystemProxyStatus
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "system_proxy.disable"}, http.MethodPost, "/v1/system-proxy/disable", request, &result)
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
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "tun.enable"}, http.MethodPost, "/v1/tun/enable", request, &result)
	return result, err
}

// DisableTun disables managed TUN via the daemon mutation path.
func (c *Client) DisableTun(ctx context.Context, request protocol.TunMutationRequest) (protocol.TunStatus, error) {
	var result protocol.TunStatus
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "tun.disable"}, http.MethodPost, "/v1/tun/disable", request, &result)
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
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "subscription.add"}, http.MethodPost, "/v1/subscriptions", request, &result)
	return result, err
}

func (c *Client) RefreshSubscription(ctx context.Context, id string, request protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "subscription.refresh"}, http.MethodPost, "/v1/subscriptions/"+url.PathEscape(id)+"/refresh", request, &result)
	return result, err
}

func (c *Client) UseSubscription(ctx context.Context, id string, request protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "subscription.use"}, http.MethodPut, "/v1/subscriptions/"+url.PathEscape(id)+"/active", request, &result)
	return result, err
}

func (c *Client) SetSubscriptionEnabled(ctx context.Context, id string, request protocol.SubscriptionEnabledRequest) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "subscription.enabled"}, http.MethodPut, "/v1/subscriptions/"+url.PathEscape(id)+"/enabled", request, &result)
	return result, err
}

func (c *Client) UpdateSubscription(ctx context.Context, id string, request protocol.SubscriptionUpdateRequest) (protocol.SubscriptionResult, error) {
	var result protocol.SubscriptionResult
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "subscription.set"}, http.MethodPatch, "/v1/subscriptions/"+url.PathEscape(id), request, &result)
	return result, err
}

func (c *Client) RemoveSubscription(ctx context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	var result protocol.MutationResult
	err := c.doMutation(ctx, logging.OperationMetadata{ID: request.OperationID, Name: "subscription.remove"}, http.MethodDelete, "/v1/subscriptions/"+url.PathEscape(id), request, &result)
	return result, err
}

func (c *Client) doMutation(ctx context.Context, operation logging.OperationMetadata, method, path string, input, output any) error {
	ctx = logging.WithOperation(ctx, operation)
	reporter := c.diagnosticReporter()
	if reporter != nil {
		reporter(ctx, diagnostics.Record{Component: "control.client", Event: "mutation_started", Level: slog.LevelDebug})
	}
	outcome := c.doRuntimeOutcome(ctx, method, path, input, output, maxControlResponseSize)
	if reporter == nil {
		return outcome.err
	}
	switch {
	case outcome.err == nil:
		reporter(ctx, diagnostics.Record{Component: "control.client", Event: "mutation_succeeded", Level: slog.LevelDebug})
	case outcome.remoteEnvelope:
		reporter(ctx, diagnostics.Record{Component: "control.client", Event: "mutation_response", Level: slog.LevelDebug, Err: outcome.err})
	default:
		if level, report := diagnostics.FailureLevel(ctx, outcome.err); report {
			reporter(ctx, diagnostics.Record{Component: "control.client", Event: "mutation_failed", Level: level, Err: outcome.err})
		}
	}
	return outcome.err
}

func (c *Client) Stream(ctx context.Context, kind string, receive func(protocol.StreamEvent) error) error {
	if receive == nil {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "stream receiver is required"}
	}
	streamURL, err := url.Parse(c.baseURL)
	if err != nil {
		return c.reportStreamOutcome(ctx, runtimeOutcome{err: diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInternal, Message: "invalid local control address"}, err)})
	}
	switch streamURL.Scheme {
	case "http":
		streamURL.Scheme = "ws"
	case "https":
		streamURL.Scheme = "wss"
	default:
		return c.reportStreamOutcome(ctx, runtimeOutcome{err: protocol.APIError{Code: protocol.CodeInternal, Message: "invalid local control address"}})
	}
	streamURL.Path = strings.TrimRight(streamURL.Path, "/") + "/v1/streams/" + url.PathEscape(kind)
	header := http.Header{}
	token, err := c.requestToken(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return c.reportStreamOutcome(ctx, runtimeOutcome{err: err})
	}
	header.Set("Authorization", "Bearer "+token)
	connection, response, err := websocket.Dial(ctx, streamURL.String(), &websocket.DialOptions{HTTPClient: c.requestHTTP(), HTTPHeader: header})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if response != nil {
			return c.reportStreamOutcome(ctx, c.responseOutcome(response))
		}
		return c.reportStreamOutcome(ctx, c.localRuntimeOutcome(err))
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
				return c.reportStreamOutcome(ctx, runtimeOutcome{err: diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "control stream message is too large"}, err)})
			}
			return c.reportStreamOutcome(ctx, runtimeOutcome{err: diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: "control stream closed unexpectedly"}, err)})
		}
		var event protocol.StreamEvent
		if err := json.Unmarshal(raw, &event); err != nil || event.Schema != "mihari/v1" || event.Stream != kind {
			return c.reportStreamOutcome(ctx, runtimeOutcome{err: diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid control stream event"}, err)})
		}
		if err := receive(event); err != nil {
			return err
		}
	}
}

// reportStreamOutcome owns local transport/decode failures and remote response
// outcomes. Callback errors belong to the caller and never enter this path.
func (c *Client) reportStreamOutcome(ctx context.Context, outcome runtimeOutcome) error {
	reporter := c.diagnosticReporter()
	if reporter == nil || diagnostics.AlreadyReported(outcome.err) {
		return outcome.err
	}
	event := "stream_failed"
	level, report := diagnostics.FailureLevel(ctx, outcome.err)
	if outcome.remoteEnvelope {
		event, level, report = "stream_response", slog.LevelDebug, true
	}
	if report {
		reporter(ctx, diagnostics.Record{Component: "control.client", Event: event, Level: level, Err: outcome.err})
		return diagnostics.MarkReported(outcome.err)
	}
	return outcome.err
}

func (c *Client) doRuntime(ctx context.Context, method, path string, input, output any) error {
	return c.doRuntimeLimit(ctx, method, path, input, output, maxControlResponseSize)
}

func (c *Client) doRuntimeLimit(ctx context.Context, method, path string, input, output any, responseLimit int64) error {
	return c.doRuntimeOutcome(ctx, method, path, input, output, responseLimit).err
}

func (c *Client) doRuntimeOutcome(ctx context.Context, method, path string, input, output any, responseLimit int64) runtimeOutcome {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return runtimeOutcome{err: protocol.APIError{Code: protocol.CodeInternal, Message: "encode control request"}}
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return runtimeOutcome{err: protocol.APIError{Code: protocol.CodeInternal, Message: "create control request"}}
	}
	token, err := c.requestToken(ctx)
	if err != nil {
		return runtimeOutcome{err: err}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.provider != nil && method != http.MethodGet && method != http.MethodHead {
		// Go's transport may replay a zero-byte failed write on a reused
		// connection when GetBody is available, even for POST. An empty body
		// also needs a non-replayable reader to prevent that retry branch.
		request.GetBody = nil
		if request.Body == nil || request.Body == http.NoBody {
			request.Body = io.NopCloser(strings.NewReader(""))
			request.ContentLength = -1
		}
	}
	response, err := c.requestHTTP().Do(request)
	if err != nil {
		return c.localRuntimeOutcome(err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return c.responseOutcome(response)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return c.localRuntimeOutcome(err)
	}
	if int64(len(raw)) > responseLimit {
		return runtimeOutcome{err: protocol.APIError{Code: protocol.CodeDataFailure, Message: "control response is too large"}}
	}
	if err := json.Unmarshal(raw, output); err != nil {
		public := protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid control response"}
		return c.localRuntimeOutcome(diagnostics.Wrap(public, err))
	}
	return runtimeOutcome{}
}

func decodeRuntimeHTTPErrorOutcome(response *http.Response) (error, bool) {
	defer response.Body.Close()
	var envelope protocol.ErrorEnvelope
	if err := json.NewDecoder(io.LimitReader(response.Body, maxControlResponseSize)).Decode(&envelope); err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid control error response"}, err), false
	}
	if envelope.Error.Code == "" {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid control error response"}, false
	}
	return envelope.Error, true
}

func (c *Client) localRuntimeOutcome(cause error) runtimeOutcome {
	public := c.localError(cause)
	var api protocol.APIError
	if !errors.As(cause, &api) && errors.As(public, &api) {
		return runtimeOutcome{err: diagnostics.Wrap(api, cause)}
	}
	return runtimeOutcome{err: public}
}
