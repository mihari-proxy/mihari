package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type Client struct {
	baseURL  string
	tokenMu  sync.RWMutex
	token    string
	http     *http.Client
	provider CredentialProvider
	classify func(error) error
	redactor *logging.Redactor
	started  bool
}

// SetRedactor binds the process redactor before the first request.
func (c *Client) SetRedactor(r *logging.Redactor) error {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if r == nil {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "control redactor is required"}
	}
	if c.started {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "control requests already started"}
	}
	c.redactor = r
	return nil
}

// CredentialProvider reads the current credential for one logical request.
// Implementations must support concurrent calls and must never fall back to a cached token.
type CredentialProvider interface {
	Load(context.Context) (string, error)
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
		baseURL:  strings.TrimRight(baseURL, "/"),
		token:    token,
		http:     httpClient,
		redactor: logging.NewRedactor(),
	}
}

// NewHTTPWithCredentialProvider adapts an injected HTTP client to per-request
// credentials. Unlike WithCredentialProvider it establishes no Unix filesystem
// or peer proof; callers choose and own the injected transport.
func NewHTTPWithCredentialProvider(baseURL string, provider CredentialProvider, httpClient *http.Client) *Client {
	c := NewHTTP(baseURL, "", httpClient)
	if provider == nil {
		provider = missingCredentialProvider{}
	}
	c.provider = provider
	return c
}

type missingCredentialProvider struct{}

func (missingCredentialProvider) Load(context.Context) (string, error) {
	return "", protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "control credential provider is required"}
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

func (c *Client) requestToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	c.started = true
	r := c.redactor
	c.tokenMu.Unlock()
	if c.provider != nil {
		token, err := c.provider.Load(ctx)
		if err != nil {
			return "", c.localError(err)
		}
		if r != nil {
			r.RetainCredential(token)
		}
		return token, nil
	}
	return c.bearerToken(), nil
}

func (c *Client) localError(err error) error {
	var api protocol.APIError
	if errors.As(err, &api) {
		return err
	}
	if c.classify != nil {
		err = c.classify(err)
		if errors.As(err, &api) {
			return err
		}
	}
	code := protocol.CodeDataFailure
	switch {
	case errors.Is(err, os.ErrPermission):
		code = protocol.CodePermissionDenied
	case errors.Is(err, os.ErrInvalid):
		code = protocol.CodeInvalidArgument
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ECONNREFUSED):
		code = protocol.CodeDaemonUnavailable
	}
	return protocol.APIError{Code: code, Message: "local control operation failed"}
}

func (c *Client) requestHTTP() *http.Client {
	if c.provider == nil {
		return c.http
	}
	copy := *c.http
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}

type authenticationError struct{ cause error }

func (e authenticationError) Error() string { return e.cause.Error() + "; " + e.Hint() }
func (e authenticationError) Unwrap() error { return e.cause }
func (e authenticationError) Hint() string {
	return "if the control credential was changed, restart the service"
}

func (c *Client) responseError(response *http.Response) error {
	err := decodeRuntimeHTTPError(response)
	var api protocol.APIError
	if c.provider != nil && response.StatusCode == http.StatusUnauthorized && errors.As(err, &api) && api.Code == protocol.CodePermissionDenied {
		return authenticationError{cause: err}
	}
	return err
}

func (c *Client) Status(ctx context.Context) (protocol.Status, error) {
	var status protocol.Status
	err := c.doRuntime(ctx, http.MethodGet, "/v1/status", nil, &status)
	return status, err
}
