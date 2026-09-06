package subscription

import (
	"encoding/hex"
	"golang.org/x/sys/unix"
)

func providerBootIdentity() (string, error) {
	b, err := unix.SysctlRaw("kern.boottime")
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
