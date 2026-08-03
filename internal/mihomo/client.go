package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

const maxResponseSize = 4 << 20

type Client struct {
	baseURL string
	secret  string
	http    *http.Client
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

func (c *Client) Reload(ctx context.Context, path string, force bool) error {
	query := url.Values{}
	query.Set("force", strconv.FormatBool(force))
	return c.do(ctx, http.MethodPut, "/configs", query, map[string]string{"path": path}, nil)
}

func (c *Client) Restart(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/restart", nil, nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return protocol.APIError{Code: protocol.CodeInternal, Message: "encode mihomo request"}
		}
		body = bytes.NewReader(encoded)
	}
	requestURL := c.baseURL + path
	if len(query) != 0 {
		requestURL += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeInternal, Message: "create mihomo request"}
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo controller is unavailable"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "read mihomo response"}
	}
	if len(raw) > maxResponseSize {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo response is too large"}
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "mihomo authentication failed"}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return protocol.APIError{
			Code:    protocol.CodeUpstreamFailure,
			Message: "mihomo request failed",
			Details: map[string]any{"status": response.StatusCode},
		}
	}
	if output == nil {
		return nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo returned an empty response"}
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo returned invalid JSON", Details: map[string]any{"cause": fmt.Sprintf("%T", err)}}
	}
	return nil
}
