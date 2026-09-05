package transport

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
)

func peerOwner(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var owner uint32
	var queryErr error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		queryErr = e
		// XNU bsd/sys/ucred.h defines the exported xucred layout version as 0.
		if e == nil {
			if cred.Version != 0 {
				queryErr = os.ErrPermission
			} else {
				owner = cred.Uid
			}
		}
	})
	return owner, errors.Join(err, queryErr)
}
