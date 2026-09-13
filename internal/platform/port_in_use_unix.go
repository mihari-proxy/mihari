//go:build !windows

package platform

import (
	"errors"
	"syscall"
)

// PortInUse identifies a socket address conflict without matching error text.
func PortInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
