package transport

import (
	"context"
	"net"
)

type Dialer func(context.Context, string) (net.Conn, error)
