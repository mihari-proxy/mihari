//go:build !windows

package config

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
