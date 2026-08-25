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

// OwnChannelWrite chowns the sidecar and newly created parent directories to the
// SUDO_USER owner when Unix euid is 0. It is a no-op on Windows.
func OwnChannelWrite(path string) error {
	return ownChannelWrite(path)
}
