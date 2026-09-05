//go:build linux || darwin

package transport

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
	"golang.org/x/sys/unix"
)

// ErrEndpointOccupied reports an endpoint that cannot safely be replaced.
var ErrEndpointOccupied = errors.New("control endpoint occupied")

type endpointScope interface {
	WithEndpoint(context.Context, platform.ResolvedLayout, func(string) error) error
}
type socketProbe func(context.Context, string) error

// ListenOwned borrows both daemon locks; the caller retains lease ownership.
func ListenOwned(ctx context.Context, layout platform.ResolvedLayout, lease *platform.OwnedDaemonLease) (net.Listener, error) {
	return listenOwned(ctx, layout, lease.Borrow(), probeSocket)
}

func listenOwned(ctx context.Context, layout platform.ResolvedLayout, scope endpointScope, probe socketProbe) (net.Listener, error) {
	var listener *ownedUnixListener
	err := scope.WithEndpoint(ctx, layout, func(endpoint string) error {
		owner := uint32(os.Geteuid())
		if layout.Mode == platform.SystemMode {
			if owner != 0 {
				return os.ErrPermission
			}
		} else if layout.Mode != platform.PrivateMode {
			return os.ErrInvalid
		}
		old, err := socketIdentity(endpoint, owner)
		if err == nil {
			probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			probeErr := probe(probeCtx, endpoint)
			cancel()
			if !errors.Is(probeErr, unix.ECONNREFUSED) {
				return ErrEndpointOccupied
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := removeSocketIdentity(endpoint, owner, old); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		l, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
		if err != nil {
			return err
		}
		l.SetUnlinkOnClose(false)
		listener = &ownedUnixListener{UnixListener: l, scope: scope, layout: layout, owner: owner}
		n, err := socketIdentity(endpoint, owner)
		if err != nil {
			return err
		}
		listener.node, listener.hasIdentity = n, true
		mode := os.FileMode(0600)
		if layout.Mode == platform.SystemMode {
			mode = 0666
		}
		if err := os.Chmod(endpoint, mode); err != nil {
			return err
		}
		after, err := socketIdentity(endpoint, owner)
		if err != nil {
			return err
		}
		if after != n {
			return platform.ErrIdentityMismatch
		}
		info, err := os.Lstat(endpoint)
		if err != nil {
			return err
		}
		if info.Mode().Perm() != mode {
			return os.ErrPermission
		}
		return nil
	})
	if err != nil {
		if listener != nil {
			err = errors.Join(err, listener.Close())
		}
		return nil, err
	}
	return listener, nil
}

type socketNode struct{ dev, ino uint64 }

func socketIdentity(endpoint string, owner uint32) (socketNode, error) {
	var st unix.Stat_t
	if err := unix.Lstat(endpoint, &st); err != nil {
		return socketNode{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Uid != owner {
		return socketNode{}, os.ErrPermission
	}
	return socketNode{uint64(st.Dev), uint64(st.Ino)}, nil
}
func removeSocketIdentity(endpoint string, owner uint32, expected socketNode) error {
	n, err := socketIdentity(endpoint, owner)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if n != expected {
		return platform.ErrIdentityMismatch
	}
	return os.Remove(endpoint)
}

type ownedUnixListener struct {
	*net.UnixListener
	scope       endpointScope
	layout      platform.ResolvedLayout
	owner       uint32
	node        socketNode
	hasIdentity bool
	once        sync.Once
	closeErr    error
}

func (l *ownedUnixListener) Close() error {
	l.once.Do(func() {
		l.closeErr = l.UnixListener.Close()
		if l.hasIdentity {
			l.closeErr = errors.Join(l.closeErr, l.scope.WithEndpoint(context.Background(), l.layout, func(endpoint string) error { return removeSocketIdentity(endpoint, l.owner, l.node) }))
		}
	})
	return l.closeErr
}

func probeSocket(ctx context.Context, endpoint string) error {
	c, err := DialContext(ctx, endpoint)
	if err != nil {
		return err
	}
	return c.Close()
}
