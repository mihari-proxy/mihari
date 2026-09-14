package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

const maxResponseSize = 4 << 20

type Client struct {
	baseURL  string
	secret   string
	http     *http.Client
	reporter diagnostics.Reporter
}

func NewClient(baseURL, secret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		http:    httpClient,
	}
}

func (c *Client) Version(ctx context.Context) (Version, error) {
	var result Version
	err := c.do(ctx, http.MethodGet, "/version", nil, nil, &result)
	return result, err
}

func (c *Client) Proxies(ctx context.Context) (Proxies, error) {
	var result Proxies
	err := c.do(ctx, http.MethodGet, "/proxies", nil, nil, &result)
	return result, err
}

func (c *Client) SelectProxy(ctx context.Context, group, name string) error {
	return c.do(ctx, http.MethodPut, "/proxies/"+url.PathEscape(group), nil, map[string]string{"name": name}, nil)
}

func (c *Client) DelayGroup(ctx context.Context, group, testURL string, timeoutMilliseconds int) (Delays, error) {
	query := url.Values{}
	query.Set("url", testURL)
	query.Set("timeout", strconv.Itoa(timeoutMilliseconds))
	var result Delays
	err := c.do(ctx, http.MethodGet, "/group/"+url.PathEscape(group)+"/delay", query, nil, &result)
	return result, err
}

func (c *Client) DelayProxy(ctx context.Context, name, testURL string, timeoutMilliseconds int) (uint16, error) {
	query := url.Values{}
	query.Set("url", testURL)
	query.Set("timeout", strconv.Itoa(timeoutMilliseconds))
	var result struct {
		Delay uint16 `json:"delay"`
	}
	err := c.do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(name)+"/delay", query, nil, &result)
	return result.Delay, err
}

func (c *Client) Connections(ctx context.Context) (Connections, error) {
	var result Connections
	err := c.do(ctx, http.MethodGet, "/connections", nil, nil, &result)
	return result, err
}

func (c *Client) CloseConnection(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/connections/"+url.PathEscape(id), nil, nil, nil)
}

func (c *Client) CloseAllConnections(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/connections", nil, nil, nil)
}

func (c *Client) Rules(ctx context.Context) (Rules, error) {
	var result Rules
	err := c.do(ctx, http.MethodGet, "/rules", nil, nil, &result)
	return result, err
}

func (c *Client) RuleProviders(ctx context.Context) (RuleProviders, error) {
	var result RuleProviders
	err := c.do(ctx, http.MethodGet, "/providers/rules", nil, nil, &result)
	return result, err
}

func (c *Client) UpdateRuleProvider(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPut, "/providers/rules/"+url.PathEscape(name), nil, nil, nil)
}

// UpdateProxyProvider asks mihomo to reload one managed local proxy provider.
func (c *Client) UpdateProxyProvider(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPut, "/providers/proxies/"+url.PathEscape(name), nil, nil, nil)
}

func (c *Client) Reload(ctx context.Context, path string, force bool) error {
	query := url.Values{}
	query.Set("force", strconv.FormatBool(force))
	return c.do(ctx, http.MethodPut, "/configs", query, map[string]string{"path": path}, nil)
}

// Configs returns the live mihomo config document (GET /configs).
func (c *Client) Configs(ctx context.Context) (map[string]any, error) {
	var result map[string]any
	err := c.do(ctx, http.MethodGet, "/configs", nil, nil, &result)
	return result, err
}

// PatchConfigs PATCHes allowlisted fields (PATCH /configs).
func (c *Client) PatchConfigs(ctx context.Context, patch map[string]any) error {
	return c.do(ctx, http.MethodPatch, "/configs", nil, patch, nil)
}

func (c *Client) Restart(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/restart", nil, nil, nil)
}

// SetDiagnosticReporter configures resource-close reporting before the client is used.
func (c *Client) SetDiagnosticReporter(reporter diagnostics.Reporter) { c.reporter = reporter }

func (c *Client) do(ctx context.Context, method, path string, query url.Values, input, output any) (resultErr error) {
	operation := diagnostics.HTTPOperation(method, path)
	requestURL := c.baseURL + path
	if len(query) != 0 {
		requestURL += "?" + query.Encode()
	}
	fail := func(code protocol.ErrorCode, message, phase string, status int, raw []byte, cause error) error {
		var details map[string]any
		if status != 0 {
			details = map[string]any{"status": status}
		}
		return diagnostics.Wrap(protocol.APIError{Code: code, Message: message, Details: details}, (&diagnostics.HTTPError{Operation: operation, URL: requestURL, Phase: phase, Status: status, Body: diagnostics.HTTPBody(raw), Cause: cause}))
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fail(protocol.CodeInternal, "encode mihomo request", "encode", 0, nil, err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return fail(protocol.CodeInternal, "create mihomo request", "request", 0, nil, err)
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fail(protocol.CodeUpstreamFailure, "mihomo controller is unavailable", "transport", 0, nil, err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			closeErr := (&diagnostics.HTTPError{Operation: operation, URL: requestURL, Phase: "close", Status: response.StatusCode, Cause: err})
			if resultErr != nil {
				var api protocol.APIError
				if errors.As(resultErr, &api) {
					resultErr = diagnostics.Wrap(api, errors.Join(resultErr, closeErr))
				}
			} else if c.reporter != nil {
				if level, emit := diagnostics.FailureLevel(ctx, closeErr); emit {
					c.reporter(ctx, diagnostics.Record{Component: "mihomo", Event: "http.close.failed", Level: min(level, slog.LevelWarn), Err: closeErr})
				}
			}
		}
	}()
	status := response.StatusCode
	readLimit := int64(maxResponseSize + 1)
	if status < 200 || status >= 300 {
		readLimit = diagnostics.MaxHTTPBodyBytes + 1
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, readLimit))
	if status < 200 || status >= 300 {
		code := protocol.CodeUpstreamFailure
		message := "mihomo request failed"
		if status == 401 || status == 403 {
			code = protocol.CodePermissionDenied
			message = "mihomo authentication failed"
		}
		resultErr = fail(code, message, "response", status, raw, readErr)
		var detail *diagnostics.HTTPError
		if errors.As(resultErr, &detail) {
			if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
				if seconds > 86400 {
					seconds = 86400
				}
				detail.RetryDelay = time.Duration(seconds) * time.Second
			} else if date, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
				detail.RetryDelay = time.Until(date)
			}
		}
		return resultErr
	}
	if readErr != nil {
		return fail(protocol.CodeUpstreamFailure, "read mihomo response", "read", status, nil, readErr)
	}
	if len(raw) > maxResponseSize {
		return fail(protocol.CodeDataFailure, "mihomo response is too large", "read", status, nil, errors.New("response exceeds 4 MiB limit"))
	}
	if output == nil {
		return nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return fail(protocol.CodeDataFailure, "mihomo returned an empty response", "decode", status, nil, io.EOF)
	}
	if err := json.Unmarshal(raw, output); err != nil {
		resultErr = fail(protocol.CodeDataFailure, "mihomo returned invalid JSON", "decode", status, nil, err)
		var api protocol.APIError
		errors.As(resultErr, &api)
		api.Details["cause"] = fmt.Sprintf("%T", err)
		return diagnostics.Wrap(api, resultErr)
	}
	return nil
}
