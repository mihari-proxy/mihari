//go:build linux || darwin

package transport

import (
	"context"
	"errors"
	"net"
	"os"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// DialVerified validates the endpoint namespace and peer before returning IO.
func DialVerified(ctx context.Context, locator platform.ControlLocator) (net.Conn, error) {
	return dialVerified(ctx, locator, platform.WithControlEndpoint, peerOwner)
}

type endpointDiscovery func(context.Context, platform.ControlLocator, func(string) error) error

func dialVerified(ctx context.Context, locator platform.ControlLocator, discover endpointDiscovery, peer func(*net.UnixConn) (uint32, error)) (net.Conn, error) {
	var connection *net.UnixConn
	err := discover(ctx, locator, func(endpoint string) error {
		c, err := DialContext(ctx, endpoint)
		if err != nil {
			return err
		}
		u, ok := c.(*net.UnixConn)
		if !ok {
			return errors.Join(os.ErrPermission, c.Close())
		}
		connection = u
		owner, err := peer(u)
		if err != nil {
			return errors.Join(os.ErrPermission, err)
		}
		if owner != locator.ExpectedOwner {
			return os.ErrPermission
		}
		return ctx.Err()
	})
	if err != nil {
		if connection != nil {
			err = errors.Join(err, connection.Close())
		}
		return nil, err
	}
	return connection, nil
}
