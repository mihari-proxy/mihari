package core

import (
	"encoding/hex"
	"golang.org/x/sys/unix"
)

func coreBootIdentity() (string, error) {
	b, e := unix.SysctlRaw("kern.boottime")
	if e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
