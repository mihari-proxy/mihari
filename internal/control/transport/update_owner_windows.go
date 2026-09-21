//go:build windows

package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"golang.org/x/sys/windows"
)

// UpdatePreparationAvailable limits the maintenance capability to Windows pipes.
const UpdatePreparationAvailable = true

type updateListener struct{ net.Listener }
type updateConn struct {
	net.Conn
	mu            sync.Mutex
	closed        bool
	acceptStarted uint64
}

func (l *updateListener) Accept() (net.Conn, error) {
	var now windows.Filetime
	windows.GetSystemTimeAsFileTime(&now)
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &updateConn{Conn: conn, acceptStarted: uint64(now.HighDateTime)<<32 | uint64(now.LowDateTime)}, nil
}

func (c *updateConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return c.Conn.Close()
}

type windowsUpdateOwner struct {
	*platform.WindowsProcessIdentity
}

func (o *windowsUpdateOwner) Key() string {
	id := o.Identity()
	return fmt.Sprintf("%d:%d:%s", id.PID, id.CreationFiletime, id.SID)
}

// OpenUpdateOwner obtains the peer PID from the retained server pipe and holds its process handle.
func OpenUpdateOwner(ctx context.Context) (UpdateOwner, error) {
	conn, ok := ctx.Value(updateConnectionKey{}).(*updateConn)
	if !ok {
		return nil, errors.New("update requires an authenticated Windows pipe connection")
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.closed {
		return nil, net.ErrClosed
	}
	fd, ok := conn.Conn.(interface{ Fd() uintptr })
	if !ok {
		return nil, errors.New("pipe handle unavailable for update authorization")
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(fd.Fd()), &pid); err != nil {
		return nil, err
	}
	owner, err := platform.OpenWindowsProcessIdentity(ctx, pid)
	if err != nil {
		return nil, err
	}
	reject := func(err error) (UpdateOwner, error) { return nil, errors.Join(err, owner.Close()) }
	// A PID reused after Accept began has a later creation time. A legitimately
	// newer client retries on a fresh connection; no guessed identity is accepted.
	if owner.Identity().CreationFiletime > conn.acceptStarted {
		return reject(protocol.APIError{Code: protocol.CodeInvalidState, Message: protocol.UpdateFreshConnectionMessage})
	}
	exited, err := owner.Exited(ctx)
	if err != nil || exited {
		return reject(errors.Join(err, errors.New("update caller has exited")))
	}
	if err := owner.AuthorizeUpdate(); err != nil {
		return reject(err)
	}
	return &windowsUpdateOwner{owner}, nil
}
