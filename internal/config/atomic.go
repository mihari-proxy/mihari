package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type replaceFunc func(source, destination string) error

type atomicWriteOps struct {
	replace replaceFunc
	syncDir func(string) error
}

// CommitResult describes whether an atomic settings write crossed its replace commit point.
type CommitResult struct {
	Committed bool
	Warning   error
}

func AtomicWrite(path string, content []byte, mode os.FileMode) error {
	result, err := AtomicWriteWithCommit(path, content, mode)
	if err != nil {
		return err
	}
	return result.Warning
}

// AtomicWriteWithCommit atomically replaces path and reports whether replacement committed.
func AtomicWriteWithCommit(path string, content []byte, mode os.FileMode) (CommitResult, error) {
	return writeAtomic(path, content, mode, atomicWriteOps{
		replace: replaceFile,
		syncDir: syncDirectory,
	})
}

func writeAtomic(path string, content []byte, mode os.FileMode, ops atomicWriteOps) (CommitResult, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return CommitResult{}, fmt.Errorf("create parent directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return CommitResult{}, fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return CommitResult{}, fmt.Errorf("set temporary permissions: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return CommitResult{}, fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return CommitResult{}, fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return CommitResult{}, fmt.Errorf("close temporary file: %w", err)
	}
	if err := ops.replace(temporaryPath, path); err != nil {
		return CommitResult{}, fmt.Errorf("replace active file: %w", err)
	}
	result := CommitResult{Committed: true}
	if err := ops.syncDir(directory); err != nil {
		result.Warning = fmt.Errorf("sync parent directory: %w", err)
	}
	return result, nil
}
