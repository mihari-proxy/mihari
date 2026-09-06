package subscription

import (
	"os"
	"strings"
)

func providerBootIdentity() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if len(id) != 36 {
		return "", os.ErrInvalid
	}
	return id, nil
}
