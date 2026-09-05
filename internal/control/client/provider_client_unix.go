//go:build linux || darwin

package client

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/platform"
	"net"
	"net/http"
	"os"
	"time"
)

// WithCredentialProvider uses verified Unix peers and a fresh credential per request.
func WithCredentialProvider(locator platform.ControlLocator, provider CredentialProvider) *Client {
	if provider == nil {
		provider = credential.NewProvider(locator)
	}
	c := NewHTTPWithCredentialProvider("http://mihari", provider, &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return transport.DialVerified(ctx, locator) },
	}, Timeout: 10 * time.Second})
	c.classify = classifyUnixError
	return c
}

func classifyUnixError(err error) error {
	switch {
	case errors.Is(err, platform.ErrUnsafeComponent), errors.Is(err, platform.ErrIdentityMismatch):
		return os.ErrPermission
	case errors.Is(err, platform.ErrLeaseConflict), errors.Is(err, transport.ErrEndpointOccupied):
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "local control resource is busy"}
	default:
		return err
	}
}
