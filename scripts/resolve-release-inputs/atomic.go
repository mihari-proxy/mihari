package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type atomicFileOps struct {
	replace       func(string, string) error
	syncDirectory func(string) error
}

type syncCloser interface {
	Sync() error
	Close() error
}

func syncAndClose(resource syncCloser) error {
	return errors.Join(resource.Sync(), resource.Close())
}

func writeAtomic(ctx context.Context, destination string, data []byte) error {
	return writeAtomicWithOps(ctx, destination, data, atomicFileOps{
		replace:       replaceFile,
		syncDirectory: syncParentDirectory,
	})
}

func writeAtomicWithOps(ctx context.Context, destination string, data []byte, operations atomicFileOps) error {
	if operations.replace == nil || operations.syncDirectory == nil {
		return errors.New("atomic release input lock operations are required")
	}
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create release input lock directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(destination)+"-*")
	if err != nil {
		return fmt.Errorf("create release input lock temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("protect release input lock temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write release input lock temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync release input lock temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close release input lock temporary file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("cancel release input lock replacement: %w", err)
	}
	if err := operations.replace(temporaryPath, destination); err != nil {
		return fmt.Errorf("replace release input lock: %w", err)
	}
	// Replacement is the commit point: returning an error after it would falsely
	// promise callers that the old reviewed lock is still active. Unix directory
	// sync is therefore best effort; Windows durability is handled by
	// MOVEFILE_WRITE_THROUGH in replaceFile.
	_ = operations.syncDirectory(directory)
	return nil
}
