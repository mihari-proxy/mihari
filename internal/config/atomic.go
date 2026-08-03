package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type replaceFunc func(source, destination string) error

func AtomicWrite(path string, content []byte, mode os.FileMode) error {
	return writeAtomic(path, content, mode, replaceFile)
}

func writeAtomic(path string, content []byte, mode os.FileMode, replace replaceFunc) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary permissions: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := replace(temporaryPath, path); err != nil {
		return fmt.Errorf("replace active file: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync parent directory: %w", err)
	}
	return nil
}
