//go:build !windows

package preferences

import "os"

func syncPreferenceDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // Read-only handle; no buffered writes remain after Sync.
	return f.Sync()
}
