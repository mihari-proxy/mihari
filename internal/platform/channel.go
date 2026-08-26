package platform

import (
	"os"
	"path/filepath"
)

// ChannelPath returns the sidecar path for the Mihari release channel.
func ChannelPath() (string, error) {
	root, err := ChannelDataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "mihari-channel"), nil
}

// ChannelDataRoot returns the data root used only for Mihari channel I/O.
// MIHARI_DATA wins. Unlike DefaultDataRoot, Unix elevated processes honor SUDO_USER.
func ChannelDataRoot() (string, error) {
	if root := os.Getenv("MIHARI_DATA"); root != "" {
		return root, nil
	}
	return channelDataRootPlatform()
}

// OwnChannelWrite chowns the sidecar to the SUDO_USER owner when Unix euid is 0.
// newParent chowns the immediate data-root directory only when this write created it.
// It is a no-op on Windows.
func OwnChannelWrite(path string, newParent bool) error {
	return ownChannelWrite(path, newParent)
}
