//go:build darwin

package app

import (
	"encoding/hex"

	"golang.org/x/sys/unix"
)

func installBootIdentity() (string, error) {
	raw, err := unix.SysctlRaw("kern.boottime")
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
