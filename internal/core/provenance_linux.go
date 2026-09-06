package core

import (
	"os"
	"strings"
)

func coreBootIdentity() (string, error) {
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return "", e
	}
	id := strings.TrimSpace(string(b))
	if len(id) != 36 {
		return "", os.ErrInvalid
	}
	return id, nil
}
