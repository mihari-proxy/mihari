//go:build !windows

package server

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func applicationUpdateIdentity(context.Context, string) (protocol.ApplicationUpdatePrepared, error) {
	return protocol.ApplicationUpdatePrepared{}, errors.New("windows application update identity unavailable")
}
