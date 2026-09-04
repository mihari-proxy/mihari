//go:build unix

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var (
	publishUnixPostLinkUnlinkFn = unix.Unlinkat
	publishUnixFsyncFn          = unix.Fsync
	publishUnixFchownFn         = unix.Fchown
)

type publishDirState struct {
	fd       int
	id       fileIdentity
	setOwner bool
	uid      int
	gid      int
}

type publishWorkspaceState struct {
	fd       int
	id       fileIdentity
	setOwner bool
	uid      int
	gid      int
}

func openPublishDir(path string) (*PublishDir, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open publish directory: %w", err)
	}
	d, err := publishDirFromFD(fd, path)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return d, nil
}

func publishDirFromFD(fd int, visiblePath string) (*PublishDir, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, fmt.Errorf("stat publish directory: %w", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, fmt.Errorf("publish path is not a directory")
	}
	canonical, err := filepath.EvalSymlinks(visiblePath)
	if err != nil {
		return nil, fmt.Errorf("resolve publish directory: %w", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, fmt.Errorf("canonicalize publish directory: %w", err)
	}
	checkFD, err := unix.Open(canonical, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("verify publish directory: %w", err)
	}
	defer unix.Close(checkFD)
	var check unix.Stat_t
	if err := unix.Fstat(checkFD, &check); err != nil {
		return nil, fmt.Errorf("verify publish directory identity: %w", err)
	}
	id := identFromStat(&st)
	if identFromStat(&check) != id {
		return nil, ErrPublishDirectoryChanged
	}
	return &PublishDir{path: filepath.Clean(canonical), plat: publishDirState{fd: fd, id: id}}, nil
}

func (d *PublishDir) existsLocked(name string) (bool, error) {
	var st unix.Stat_t
	err := unix.Fstatat(d.plat.fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check publish target: %w", err)
	}
	return true, nil
}

func (d *PublishDir) isWithinLocked(ancestor *DirectoryIdentity) (bool, error) {
	current, err := dupCLOEXEC(d.plat.fd)
	if err != nil {
		return false, fmt.Errorf("duplicate publish directory: %w", err)
	}
	defer func() {
		if current >= 0 {
			_ = unix.Close(current)
		}
	}()
	for {
		var currentStat unix.Stat_t
		if err := unix.Fstat(current, &currentStat); err != nil {
			return false, fmt.Errorf("stat publish directory ancestry: %w", err)
		}
		currentID := identFromStat(&currentStat)
		if currentID == ancestor.plat.id {
			return true, nil
		}
		parent, err := unix.Openat(current, "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return false, fmt.Errorf("open publish directory parent: %w", err)
		}
		var parentStat unix.Stat_t
		if err := unix.Fstat(parent, &parentStat); err != nil {
			_ = unix.Close(parent)
			return false, fmt.Errorf("stat publish directory parent: %w", err)
		}
		if identFromStat(&parentStat) == currentID {
			_ = unix.Close(parent)
			return false, nil
		}
		if err := unix.Close(current); err != nil {
			_ = unix.Close(parent)
			return false, fmt.Errorf("close publish ancestry handle: %w", err)
		}
		current = parent
	}
}

func (d *PublishDir) createWorkspaceLocked() (*PublishWorkspace, error) {
	for i := 0; i < 100; i++ {
		name, err := randomTempName(".mihari-export-*")
		if err != nil {
			return nil, err
		}
		if err := unix.Mkdirat(d.plat.fd, name, 0o700); err != nil {
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			return nil, fmt.Errorf("create publish workspace: %w", err)
		}
		fd, err := unix.Openat(d.plat.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			_ = unix.Unlinkat(d.plat.fd, name, unix.AT_REMOVEDIR)
			return nil, fmt.Errorf("open publish workspace: %w", err)
		}
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(d.plat.fd, name, unix.AT_REMOVEDIR)
			return nil, fmt.Errorf("stat publish workspace: %w", err)
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(d.plat.fd, name, unix.AT_REMOVEDIR)
			return nil, fmt.Errorf("publish workspace is not a directory")
		}
		if err := unix.Fchmod(fd, 0o700); err != nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(d.plat.fd, name, unix.AT_REMOVEDIR)
			return nil, fmt.Errorf("harden publish workspace: %w", err)
		}
		if d.plat.setOwner && effectiveUID() == 0 {
			if err := publishUnixFchownFn(fd, d.plat.uid, d.plat.gid); err != nil {
				_ = unix.Close(fd)
				_ = unix.Unlinkat(d.plat.fd, name, unix.AT_REMOVEDIR)
				return nil, fmt.Errorf("set publish workspace owner: %w", err)
			}
		}
		return &PublishWorkspace{owner: d, name: name, plat: publishWorkspaceState{
			fd: fd, id: identFromStat(&st), setOwner: d.plat.setOwner, uid: d.plat.uid, gid: d.plat.gid,
		}}, nil
	}
	return nil, fmt.Errorf("create publish workspace: exhausted names")
}

func (w *PublishWorkspace) createTempLocked(pattern string) (*os.File, string, error) {
	for i := 0; i < 100; i++ {
		name, err := randomTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		fd, err := unix.Openat(w.plat.fd, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != nil {
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			return nil, "", fmt.Errorf("create publish temp: %w", err)
		}
		if err := unix.Fchmod(fd, 0o600); err != nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(w.plat.fd, name, 0)
			return nil, "", fmt.Errorf("harden publish temp: %w", err)
		}
		if w.plat.setOwner && effectiveUID() == 0 {
			if err := publishUnixFchownFn(fd, w.plat.uid, w.plat.gid); err != nil {
				_ = unix.Close(fd)
				_ = unix.Unlinkat(w.plat.fd, name, 0)
				return nil, "", fmt.Errorf("set publish temp owner: %w", err)
			}
		}
		return os.NewFile(uintptr(fd), name), name, nil
	}
	return nil, "", fmt.Errorf("create publish temp: exhausted names")
}

func (w *PublishWorkspace) removeLocked(name string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(w.plat.fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("remove publish temp: %w", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("remove publish temp: not a regular file")
	}
	if err := unix.Unlinkat(w.plat.fd, name, 0); err != nil {
		return fmt.Errorf("remove publish temp: %w", err)
	}
	return nil
}

func (d *PublishDir) publishNoReplaceLocked(w *PublishWorkspace, tempName, targetName string, warn func(error)) error {
	checkFD, err := unix.Open(d.path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("%w", ErrPublishDirectoryChanged)
	}
	var st unix.Stat_t
	statErr := unix.Fstat(checkFD, &st)
	closeErr := unix.Close(checkFD)
	if statErr != nil || identFromStat(&st) != d.plat.id {
		return fmt.Errorf("%w", ErrPublishDirectoryChanged)
	}
	if closeErr != nil {
		return fmt.Errorf("verify publish directory: %w", closeErr)
	}
	if err := unix.Linkat(w.plat.fd, tempName, d.plat.fd, targetName, 0); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("publish target: %w", os.ErrExist)
		}
		return fmt.Errorf("publish target: %w", err)
	}
	if err := publishUnixPostLinkUnlinkFn(w.plat.fd, tempName, 0); err != nil {
		warn(fmt.Errorf("remove published workspace temp: %w", err))
	}
	if err := publishUnixFsyncFn(w.plat.fd); err != nil {
		warn(fmt.Errorf("sync publish workspace: %w", err))
	}
	if err := publishUnixFsyncFn(d.plat.fd); err != nil {
		warn(fmt.Errorf("sync publish directory: %w", err))
	}
	return nil
}

func (w *PublishWorkspace) closePlatformLocked(owner *PublishDir) error {
	if w.plat.fd < 0 {
		return nil
	}
	var cleanupErr error
	if owner == nil || owner.closed || owner.plat.fd < 0 {
		cleanupErr = fmt.Errorf("publish workspace cleanup: parent is closed")
	} else {
		name, err := findUnixEntryByIdentity(owner.plat.fd, w.plat.id)
		if err != nil {
			cleanupErr = err
		} else if name == "" {
			cleanupErr = fmt.Errorf("publish workspace cleanup: workspace moved outside parent")
		} else {
			publishWorkspaceCleanupCheckpoint()
			verifiedName, verifyErr := findUnixEntryByIdentity(owner.plat.fd, w.plat.id)
			if verifyErr != nil {
				cleanupErr = verifyErr
			} else if verifiedName != name {
				cleanupErr = fmt.Errorf("publish workspace cleanup: workspace identity changed before deletion")
			} else if safe, safeErr := unixParentHasPrivateMutationBoundary(owner.plat.fd); safeErr != nil {
				cleanupErr = safeErr
			} else if !safe {
				cleanupErr = fmt.Errorf("publish workspace cleanup: parent permits untrusted entry replacement")
			} else if err := unix.Unlinkat(owner.plat.fd, name, unix.AT_REMOVEDIR); err != nil {
				cleanupErr = fmt.Errorf("remove publish workspace: %w", err)
			}
		}
	}
	closeErr := unix.Close(w.plat.fd)
	w.plat.fd = -1
	return errors.Join(cleanupErr, closeErr)
}

func unixParentHasPrivateMutationBoundary(parentFD int) (bool, error) {
	var st unix.Stat_t
	if err := unix.Fstat(parentFD, &st); err != nil {
		return false, fmt.Errorf("stat publish parent permissions: %w", err)
	}
	return st.Mode&0o022 == 0, nil
}

func (d *PublishDir) closePlatformLocked() error {
	if d.plat.fd < 0 {
		return nil
	}
	err := unix.Close(d.plat.fd)
	d.plat.fd = -1
	return err
}

func findUnixEntryByIdentity(parentFD int, want fileIdentity) (string, error) {
	listFD, err := unix.Openat(parentFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", fmt.Errorf("list publish parent: %w", err)
	}
	defer unix.Close(listFD)
	names, err := readUnixDirNames(listFD)
	if err != nil {
		return "", fmt.Errorf("list publish parent: %w", err)
	}
	for _, name := range names {
		var st unix.Stat_t
		if err := unix.Fstatat(parentFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return "", fmt.Errorf("stat publish workspace entry: %w", err)
		}
		if st.Mode&unix.S_IFMT == unix.S_IFDIR && identFromStat(&st) == want {
			return name, nil
		}
	}
	return "", nil
}
