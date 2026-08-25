//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

func channelDataRootPlatform() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve mihari channel data root: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("resolve mihari channel data root: empty home")
	}
	return filepath.Join(home, ".mihari"), nil
}

func ownChannelWrite(string) error {
	return nil
}
