//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fileAddFile         = 0x0002
	fileAddSubdirectory = 0x0004
	publishDirAccess    = windows.FILE_LIST_DIRECTORY | fileAddFile | fileAddSubdirectory |
		windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
)

var (
	publishWindowsPostRenameFlushFn         = windows.FlushFileBuffers
	publishWindowsCreatedHandleAttributesFn = handleAttributes
	publishWindowsDeleteCreatedFn           = markWindowsHandleForDeletion
)

type publishDirState struct {
	handle windows.Handle
	id     fileIdentity
}

type publishWorkspaceState struct {
	handle windows.Handle
	id     fileIdentity
}

func openPublishDir(path string) (*PublishDir, error) {
	h, err := openNTPath(path, publishDirAccess, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT, windows.FILE_ATTRIBUTE_DIRECTORY, nil)
	if err != nil {
		return nil, fmt.Errorf("open publish directory: %w", err)
	}
	d, err := publishDirFromHandle(h)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	return d, nil
}

func publishDirFromHandle(h windows.Handle) (*PublishDir, error) {
	attr, err := handleAttributes(h)
	if err != nil {
		return nil, fmt.Errorf("stat publish directory: %w", err)
	}
	if attr&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return nil, fmt.Errorf("publish path is not a directory")
	}
	id, err := identityFromHandle(h)
	if err != nil {
		return nil, fmt.Errorf("identify publish directory: %w", err)
	}
	canonical, err := finalPathFromHandle(h)
	if err != nil {
		return nil, fmt.Errorf("canonicalize publish directory: %w", err)
	}
	return &PublishDir{path: canonical, plat: publishDirState{handle: h, id: id}}, nil
}

func (d *PublishDir) existsLocked(name string) (bool, error) {
	h, err := openRelative(d.plat.handle, name, windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
	if isWindowsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check publish target: %w", err)
	}
	if err := windows.CloseHandle(h); err != nil {
		return false, fmt.Errorf("close publish target probe: %w", err)
	}
	return true, nil
}

func (d *PublishDir) isWithinLocked(ancestor *DirectoryIdentity) (bool, error) {
	aID1, aName1, err := windowsDirectoryState(ancestor.plat.handle)
	if err != nil {
		return false, err
	}
	tID1, tName1, err := windowsDirectoryState(d.plat.handle)
	if err != nil {
		return false, err
	}
	aID2, aName2, err := windowsDirectoryState(ancestor.plat.handle)
	if err != nil {
		return false, err
	}
	tID2, tName2, err := windowsDirectoryState(d.plat.handle)
	if err != nil {
		return false, err
	}
	if aID1 != aID2 || tID1 != tID2 || aID1 != ancestor.plat.id || tID1 != d.plat.id ||
		!strings.EqualFold(aName1, aName2) || !strings.EqualFold(tName1, tName2) {
		return false, fmt.Errorf("publish directory identity was unstable")
	}
	if aID1.volume != tID1.volume {
		return false, nil
	}
	ancestorPath := strings.TrimRight(filepath.Clean(aName1), `\/`)
	targetPath := strings.TrimRight(filepath.Clean(tName1), `\/`)
	if strings.EqualFold(ancestorPath, targetPath) {
		return true, nil
	}
	return strings.HasPrefix(strings.ToLower(targetPath), strings.ToLower(ancestorPath+string(filepath.Separator))), nil
}

func windowsDirectoryState(h windows.Handle) (fileIdentity, string, error) {
	id, err := identityFromHandle(h)
	if err != nil {
		return fileIdentity{}, "", fmt.Errorf("identify held directory: %w", err)
	}
	name, err := finalPathFromHandle(h)
	if err != nil {
		return fileIdentity{}, "", fmt.Errorf("name held directory: %w", err)
	}
	return id, name, nil
}

func (d *PublishDir) createWorkspaceLocked() (*PublishWorkspace, error) {
	sddl, err := publishSDDL(true)
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE | windows.WRITE_DAC)
	for i := 0; i < 100; i++ {
		name, err := randomTempName(".mihari-export-*")
		if err != nil {
			return nil, err
		}
		h, err := openRelative(d.plat.handle, name, access, windows.FILE_CREATE,
			windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
			windows.FILE_ATTRIBUTE_DIRECTORY, sd)
		if err != nil {
			if isWindowsExist(err) {
				continue
			}
			return nil, fmt.Errorf("create publish workspace: %w", err)
		}
		if err := rejectCreatedWindowsReparse(h, name); err != nil {
			return nil, errors.Join(err, cleanupCreatedWindowsHandle(h))
		}
		if err := hardenHandle(h, sddl); err != nil {
			_ = markWindowsHandleForDeletion(h)
			_ = windows.CloseHandle(h)
			return nil, fmt.Errorf("harden publish workspace: %w", err)
		}
		id, err := identityFromHandle(h)
		if err != nil {
			_ = markWindowsHandleForDeletion(h)
			_ = windows.CloseHandle(h)
			return nil, err
		}
		return &PublishWorkspace{owner: d, name: name, plat: publishWorkspaceState{handle: h, id: id}}, nil
	}
	return nil, fmt.Errorf("create publish workspace: exhausted names")
}

func (w *PublishWorkspace) createTempLocked(pattern string) (*os.File, string, error) {
	sddl, err := publishSDDL(false)
	if err != nil {
		return nil, "", err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, "", err
	}
	access := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.DELETE | windows.WRITE_DAC)
	for i := 0; i < 100; i++ {
		name, err := randomTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		h, err := openRelative(w.plat.handle, name, access, windows.FILE_CREATE,
			windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
			windows.FILE_ATTRIBUTE_NORMAL, sd)
		if err != nil {
			if isWindowsExist(err) {
				continue
			}
			return nil, "", fmt.Errorf("create publish temp: %w", err)
		}
		if err := rejectCreatedWindowsReparse(h, name); err != nil {
			return nil, "", errors.Join(err, cleanupCreatedWindowsHandle(h))
		}
		if err := hardenHandle(h, sddl); err != nil {
			_ = markWindowsHandleForDeletion(h)
			_ = windows.CloseHandle(h)
			return nil, "", fmt.Errorf("harden publish temp: %w", err)
		}
		return os.NewFile(uintptr(h), name), name, nil
	}
	return nil, "", fmt.Errorf("create publish temp: exhausted names")
}

func (w *PublishWorkspace) removeLocked(name string) error {
	h, err := openRelative(w.plat.handle, name, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
	if err != nil {
		return fmt.Errorf("remove publish temp: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := rejectReparse(h, name); err != nil {
		return err
	}
	if err := markWindowsHandleForDeletion(h); err != nil {
		return fmt.Errorf("remove publish temp: %w", err)
	}
	return nil
}

func (d *PublishDir) publishNoReplaceLocked(w *PublishWorkspace, tempName, targetName string, warn func(error)) error {
	check, err := openNTPath(d.path, windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT, windows.FILE_ATTRIBUTE_DIRECTORY, nil)
	if err != nil {
		return fmt.Errorf("%w", ErrPublishDirectoryChanged)
	}
	checkID, idErr := identityFromHandle(check)
	closeErr := windows.CloseHandle(check)
	if idErr != nil || checkID != d.plat.id {
		return fmt.Errorf("%w", ErrPublishDirectoryChanged)
	}
	if closeErr != nil {
		return fmt.Errorf("verify publish directory: %w", closeErr)
	}
	h, err := openRelative(w.plat.handle, tempName, windows.DELETE|windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
	if err != nil {
		return fmt.Errorf("open publish temp: %w", err)
	}
	if err := rejectReparse(h, tempName); err != nil {
		_ = windows.CloseHandle(h)
		return err
	}
	if err := renameHandle(h, d.plat.handle, targetName, windows.FILE_RENAME_POSIX_SEMANTICS); err != nil {
		_ = windows.CloseHandle(h)
		if isWindowsExist(err) {
			return fmt.Errorf("publish target: %w", os.ErrExist)
		}
		return fmt.Errorf("publish target: %w", err)
	}
	if err := publishWindowsPostRenameFlushFn(h); err != nil {
		warn(fmt.Errorf("flush published target: %w", err))
	}
	if err := windows.CloseHandle(h); err != nil {
		warn(fmt.Errorf("close published target: %w", err))
	}
	return nil
}

func (w *PublishWorkspace) closePlatformLocked(owner *PublishDir) error {
	if w.plat.handle == 0 {
		return nil
	}
	var cleanupErr error
	if owner == nil || owner.closed || owner.plat.handle == 0 {
		cleanupErr = fmt.Errorf("publish workspace cleanup: parent is closed")
	} else {
		name, err := windowsFindEntryByIdentity(owner.plat.handle, w.plat.id)
		if err != nil {
			cleanupErr = err
		} else if name == "" {
			cleanupErr = fmt.Errorf("publish workspace cleanup: workspace moved outside parent")
		} else {
			publishWorkspaceCleanupCheckpoint()
			verifiedName, verifyErr := windowsFindEntryByIdentity(owner.plat.handle, w.plat.id)
			if verifyErr != nil {
				cleanupErr = verifyErr
			} else if !strings.EqualFold(verifiedName, name) {
				cleanupErr = fmt.Errorf("publish workspace cleanup: workspace identity changed before deletion")
			} else if empty, emptyErr := windowsDirectoryEmpty(w.plat.handle); emptyErr != nil {
				cleanupErr = emptyErr
			} else if !empty {
				cleanupErr = fmt.Errorf("remove publish workspace: directory not empty")
			} else if markErr := markWindowsHandleDeletePending(w.plat.handle, true); markErr != nil {
				cleanupErr = fmt.Errorf("remove publish workspace: %w", markErr)
			} else if contained, containErr := windowsWorkspaceStillInParent(owner.plat.handle, w.plat.handle, name); containErr != nil || !contained {
				clearErr := markWindowsHandleDeletePending(w.plat.handle, false)
				cleanupErr = errors.Join(fmt.Errorf("publish workspace cleanup: workspace moved outside parent"), containErr, clearErr)
			}
		}
	}
	closeErr := windows.CloseHandle(w.plat.handle)
	w.plat.handle = 0
	return errors.Join(cleanupErr, closeErr)
}

func (d *PublishDir) closePlatformLocked() error {
	if d.plat.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(d.plat.handle)
	d.plat.handle = 0
	return err
}

func publishSDDL(directory bool) (string, error) {
	user, err := currentUserSID()
	if err != nil {
		return "", fmt.Errorf("query interactive user SID: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return "", err
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	if user.Equals(system) {
		return "D:P(A;" + inherit + ";FA;;;SY)", nil
	}
	return "D:P(A;" + inherit + ";FA;;;" + user.String() + ")(A;" + inherit + ";FA;;;SY)", nil
}

func finalPathFromHandle(h windows.Handle) (string, error) {
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buf)) {
			path := windows.UTF16ToString(buf[:n])
			switch {
			case strings.HasPrefix(path, `\\?\UNC\`):
				path = `\\` + path[len(`\\?\UNC\`):]
			case strings.HasPrefix(path, `\\?\`):
				path = path[len(`\\?\`):]
			}
			return filepath.Clean(path), nil
		}
		buf = make([]uint16, n+1)
	}
}

func windowsFindEntryByIdentity(parent windows.Handle, want fileIdentity) (string, error) {
	list, err := openListingHandle(parent)
	if err != nil {
		return "", fmt.Errorf("list publish parent: %w", err)
	}
	defer windows.CloseHandle(list)
	entries, err := readWindowsDirents(list)
	if err != nil {
		return "", fmt.Errorf("list publish parent: %w", err)
	}
	for _, entry := range entries {
		h, err := openRelative(parent, entry.name, windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN,
			windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
		if isWindowsNotFound(err) {
			continue
		}
		if err != nil {
			continue
		}
		attr, attrErr := handleAttributes(h)
		id, idErr := identityFromHandle(h)
		_ = windows.CloseHandle(h)
		if attrErr == nil && idErr == nil && attr&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 && id == want {
			return entry.name, nil
		}
	}
	return "", nil
}

func windowsWorkspaceStillInParent(parent, workspace windows.Handle, name string) (bool, error) {
	parentPath, err := finalPathFromHandle(parent)
	if err != nil {
		return false, err
	}
	workspacePath, err := finalPathFromHandle(workspace)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(filepath.Dir(workspacePath), parentPath) && strings.EqualFold(filepath.Base(workspacePath), name), nil
}

func windowsDirectoryEmpty(h windows.Handle) (bool, error) {
	list, err := openListingHandle(h)
	if err != nil {
		return false, fmt.Errorf("list publish workspace: %w", err)
	}
	defer windows.CloseHandle(list)
	entries, err := readWindowsDirents(list)
	if err != nil {
		return false, fmt.Errorf("list publish workspace: %w", err)
	}
	return len(entries) == 0, nil
}

func markWindowsHandleForDeletion(h windows.Handle) error {
	info := uint32(windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS)
	var iosb windows.IO_STATUS_BLOCK
	err := windows.NtSetInformationFile(h, &iosb, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), windows.FileDispositionInformationEx)
	runtime.KeepAlive(&info)
	return err
}

func markWindowsHandleDeletePending(h windows.Handle, delete bool) error {
	var info byte
	if delete {
		info = 1
	}
	var iosb windows.IO_STATUS_BLOCK
	err := windows.NtSetInformationFile(h, &iosb, &info, 1, windows.FileDispositionInformation)
	runtime.KeepAlive(&info)
	return err
}

func rejectCreatedWindowsReparse(h windows.Handle, name string) error {
	attr, err := publishWindowsCreatedHandleAttributesFn(h)
	if err != nil {
		return fmt.Errorf("inspect newly created %s: %w", name, err)
	}
	if attr&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%s: reparse point or junction is not allowed", name)
	}
	return nil
}

func cleanupCreatedWindowsHandle(h windows.Handle) error {
	deleteErr := publishWindowsDeleteCreatedFn(h)
	closeErr := windows.CloseHandle(h)
	if deleteErr != nil {
		deleteErr = fmt.Errorf("delete rejected created object: %w", deleteErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close rejected created object: %w", closeErr)
	}
	return errors.Join(deleteErr, closeErr)
}
