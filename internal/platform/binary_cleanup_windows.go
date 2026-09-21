//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type binaryMaintenance struct {
	directories []windows.Handle
	lock        windows.Handle
	overlapped  windows.Overlapped
}

// LockBinaryMaintenance serializes replacement and cleanup in one verified program directory.
// Directory handles deny rename until the owner releases the lock.
func LockBinaryMaintenance(ctx context.Context, target string) (_ io.Closer, err error) {
	m := &binaryMaintenance{}
	defer func() {
		if err != nil {
			err = errors.Join(err, m.Close())
		}
	}()
	if !filepath.IsAbs(target) {
		return nil, os.ErrInvalid
	}
	dir := filepath.Dir(filepath.Clean(target))
	volume := filepath.VolumeName(dir)
	if strings.HasPrefix(volume, `\\`) {
		return nil, ErrUnsafeComponent
	}
	root := volume + string(os.PathSeparator)
	h, err := replacementWindowsOpen(root, false)
	if err != nil {
		return nil, err
	}
	m.directories = append(m.directories, h)
	user, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	check := func(h windows.Handle) error {
		if e := rejectReparse(h, dir); e != nil {
			return e
		}
		sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		// Maintenance may operate in the calling user's own portable directory;
		// it never executes a discovered file or trusts another user's writable path.
		if e != nil {
			return e
		}
		if !maintenanceWindowsTrust(sd, user) {
			name, _ := finalPathFromHandle(h) // Diagnostic only; rejection does not depend on rendering the path.
			return fmt.Errorf("verify maintenance permissions on %s: %w", name, ErrUnsafeComponent)
		}
		return nil
	}
	for _, part := range strings.Split(strings.TrimPrefix(dir, root), string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		h, err = openRelativeWithShare(h, part, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
		if err != nil {
			return nil, err
		}
		m.directories = append(m.directories, h)
		if err = rejectReparse(h, part); err != nil {
			return nil, err
		}
	}
	// Ancestors are pinned without delete sharing; only this owned directory
	// grants deletion authority. Shared ancestors (e.g. Temp) grant none.
	if err = check(h); err != nil {
		return nil, err
	}
	m.lock, err = openRelativeWithShare(h, ".mihari-binary.lock", windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL|windows.SYNCHRONIZE, windows.FILE_OPEN_IF, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, windows.FILE_ATTRIBUTE_NORMAL, nil, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if err != nil {
		return nil, err
	}
	if err = check(m.lock); err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(m.lock, &info); err != nil {
		return nil, err
	}
	if info.NumberOfLinks != 1 {
		return nil, ErrUnsafeComponent
	}
	for {
		err = windows.LockFileEx(m.lock, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &m.overlapped)
		if err == nil {
			return m, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// A LocalSystem daemon can maintain its private data directory for the one
// individual user already authorized by that directory. Never accept group
// writers or multiple users, and never rewrite the existing ACL.
func maintenanceWindowsTrust(sd *windows.SECURITY_DESCRIPTOR, caller *windows.SID) bool {
	if replacementWindowsExecutionTrust(sd, false, caller, false) {
		return true
	}
	if caller.String() != "S-1-5-18" {
		return false
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return false
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil {
			return false
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		kind, e := privateAccountKind(sid)
		if e == nil && kind == windows.SidTypeUser && sid.String() != "S-1-5-18" && replacementWindowsExecutionTrust(sd, false, sid, false) {
			return true
		}
	}
	return false
}

func (m *binaryMaintenance) Close() error {
	var err error
	if m.lock != 0 {
		err = windows.CloseHandle(m.lock)
		m.lock = 0
	} // Closing releases any byte-range lock, including failed acquisition.
	for i := len(m.directories) - 1; i >= 0; i-- {
		err = errors.Join(err, windows.CloseHandle(m.directories[i]))
	}
	m.directories = nil
	return err
}

// CleanupBinaryStashes removes obsolete Windows update files beside target.
func CleanupBinaryStashes(ctx context.Context, target string) (err error) {
	// A busy updater must never delay the normal startup/Ready path indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	owner, err := LockBinaryMaintenance(ctx, target)
	if err != nil {
		return fmt.Errorf("lock cleanup for %s: %w", target, err)
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	m := owner.(*binaryMaintenance)
	parent := m.directories[len(m.directories)-1]
	listing, err := openListingHandle(parent)
	if err != nil {
		return err
	}
	entries, readErr := readBinaryCleanupEntries(ctx, listing)
	if err = errors.Join(readErr, windows.CloseHandle(listing)); err != nil {
		return err
	}
	prefix := filepath.Base(target) + ".old-"
	for _, entry := range entries {
		if e := ctx.Err(); e != nil {
			return errors.Join(err, e)
		}
		if len(entry.name) < len(prefix) || !strings.EqualFold(entry.name[:len(prefix)], prefix) {
			continue
		}
		suffix := entry.name[len(prefix):]
		stamp, e := strconv.ParseInt(suffix, 10, 64)
		if e != nil || stamp <= 0 || strconv.FormatInt(stamp, 10) != suffix {
			continue
		}
		e = removeBinaryStash(parent, entry.name)
		if e != nil {
			err = errors.Join(err, fmt.Errorf("remove %s: %w", filepath.Join(filepath.Dir(target), entry.name), e))
		}
	}
	return err
}

func readBinaryCleanupEntries(ctx context.Context, h windows.Handle) ([]windowsDirent, error) {
	const maxEntries = 4096
	buffer := make([]byte, 64<<10)
	var entries []windowsDirent
	for first := true; ; first = false {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		class := uint32(windows.FileFullDirectoryInfo)
		if first {
			class = windows.FileFullDirectoryRestartInfo
		}
		err := windows.GetFileInformationByHandleEx(h, class, &buffer[0], uint32(len(buffer)))
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) || errors.Is(err, windows.ERROR_HANDLE_EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, parseFullDirInfo(buffer)...)
		if len(entries) > maxEntries {
			return nil, fmt.Errorf("binary cleanup directory exceeds %d entries", maxEntries)
		}
	}
}

func removeBinaryStash(parent windows.Handle, name string) (err error) {
	h, err := openRelativeWithShare(parent, name, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil, windows.FILE_SHARE_READ)
	if isWindowsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	if err = rejectReparse(h, name); err != nil {
		return err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return ErrUnsafeComponent
	}
	// Delete the verified opened object, never re-resolve a pathname after checking it.
	// Ordinary disposition preserves Windows' refusal to delete a mapped executable.
	return markWindowsHandleDeletePending(h, true)
}
