package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
)

type Client struct {
	baseURL string
	tokenMu sync.RWMutex
	token   string
	http    *http.Client
}

func New(endpoint, token string) *Client {
	transportClient := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return transport.DialContext(ctx, endpoint)
		},
	}
	return NewHTTP("http://mihari", token, &http.Client{
		Transport: transportClient,
		Timeout:   10 * time.Second,
	})
}

func NewHTTP(baseURL, token string, httpClient *http.Client) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    httpClient,
	}
}

// SetToken replaces the Bearer token. An empty token is a no-op.
func (c *Client) SetToken(token string) {
	if token == "" {
		return
	}
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = token
}

func (c *Client) bearerToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

func (c *Client) Status(ctx context.Context) (protocol.Status, error) {
	var status protocol.Status
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/status", nil)
	if err != nil {
		return status, err
	}
	request.Header.Set("Authorization", "Bearer "+c.bearerToken())

	response, err := c.http.Do(request)
	if err != nil {
		return status, fmt.Errorf("control request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var envelope protocol.ErrorEnvelope
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
			return status, fmt.Errorf("control response %s", response.Status)
		}
		return status, envelope.Error
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return status, fmt.Errorf("decode status: %w", err)
	}
	return status, nil
}
