package transport

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
)

func peerOwner(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var owner uint32
	var queryErr error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		queryErr = e
		if e == nil {
			owner = cred.Uid
		}
	})
	return owner, errors.Join(err, queryErr)
}
