//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// CleanupReplacedBinary removes retired copies beside an installed binary.
// It never forces deletion of mapped images or traverses matching directories.
// Holding the current target without delete sharing excludes the rename/rollback
// window of a concurrent replacement. A missing target retains all backups.
func CleanupReplacedBinary(ctx context.Context, binary string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(binary) {
		return fmt.Errorf("cleanup binary path must be absolute: %s", binary)
	}
	parent, err := openNTPath(filepath.Dir(binary), windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
	if isWindowsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open cleanup directory %s: %w", filepath.Dir(binary), err)
	}
	defer func() { resultErr = errors.Join(resultErr, windows.CloseHandle(parent)) }()
	if err := rejectReparse(parent, filepath.Dir(binary)); err != nil {
		return err
	}
	flags := uint32(windows.FILE_NON_DIRECTORY_FILE | windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	current, err := openRelativeWithShare(parent, filepath.Base(binary), windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, flags, 0, nil, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if isWindowsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("hold current binary %s for cleanup: %w", binary, err)
	}
	defer func() { resultErr = errors.Join(resultErr, windows.CloseHandle(current)) }()
	if err := rejectReparse(current, binary); err != nil {
		return err
	}
	entries, err := readWindowsDirents(parent)
	if err != nil {
		return fmt.Errorf("list old binaries beside %s: %w", binary, err)
	}
	prefix := filepath.Base(binary) + ".old-"
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return errors.Join(resultErr, err)
		}
		if !strings.HasPrefix(entry.name, prefix) || entry.attr&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
			continue
		}
		suffix := strings.TrimPrefix(entry.name, prefix)
		stamp, parseErr := strconv.ParseInt(suffix, 10, 64)
		if parseErr != nil || stamp <= 0 || strconv.FormatInt(stamp, 10) != suffix {
			continue
		}
		err := removeRetiredBinary(parent, entry.name, flags)
		if err != nil && !isWindowsNotFound(err) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove old binary %s: %w", filepath.Join(filepath.Dir(binary), entry.name), err))
		}
	}
	return resultErr
}

func removeRetiredBinary(parent windows.Handle, name string, flags uint32) (resultErr error) {
	h, err := openRelative(parent, name, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, flags, 0, nil)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, windows.CloseHandle(h)) }()
	if err := rejectReparse(h, name); err != nil {
		return err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("refuse hard-linked old binary %s", name)
	}
	// Ordinary delete disposition respects open images and sharing restrictions.
	return markWindowsHandleDeletePending(h, true)
}
