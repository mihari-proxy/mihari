//go:build !linux && !darwin

package app

import (
	"context"
	"io"
	"os"
)

func openMigrationCapability(_ context.Context, path string, create bool) (migrationCapability, error) {
	if create {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, err
		}
	}
	return openDirCap(path), nil
}

func readHostFile(path string, max int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errMigrationOversize
	}
	return data, nil
}
