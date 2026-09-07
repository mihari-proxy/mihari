//go:build linux

package app

import (
	"os"
	"strings"
)

func installBootIdentity() (string, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	if len(id) != 36 {
		return "", os.ErrInvalid
	}
	return id, nil
}
