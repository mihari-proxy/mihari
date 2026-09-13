package platform

import (
	"errors"
	"syscall"
)

// PortInUse identifies Windows socket address conflicts without matching text.
func PortInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.Errno(10048))
}
