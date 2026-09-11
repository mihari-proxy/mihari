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
	publishUnixWorkspaceOpenFn  = unix.Openat
	publishUnixPostLinkUnlinkFn = unix.Unlinkat
	publishUnixFsyncFn          = unix.Fsync
	publishUnixFchownFn         = unix.Fchown
	publishUnixACLBoundaryFn    = unixACLHasNoAdditionalAuthority
	publishUnixCleanupUnlinkFn  = unix.Unlinkat
	publishUnixCleanupReadFn    = readUnixDirNames
	publishUnixCleanupCloseFn   = unix.Close
	publishUnixVerifyStatFn     = unix.Fstat
	publishUnixVerifyCloseFn    = unix.Close
)

type publishDirState struct {
	trusted                 *TrustedRoot
	fd                      int
	id                      fileIdentity
	setOwner                bool
	uid                     int
	gid                     int
	initialNamespaceTrusted bool
	initialNamespaceErr     error
}

type publishWorkspaceState struct {
	trusted    *TrustedRoot
	identities map[string]FileIdentity
	fd         int
	id         fileIdentity
	setOwner   bool
	uid        int
	gid        int
	created    map[string]struct{}
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
	d.assessInitialNamespace()
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
	var check unix.Stat_t
	if err := unix.Fstat(checkFD, &check); err != nil {
		return nil, errors.Join(fmt.Errorf("verify publish directory identity: %w", err), unix.Close(checkFD))
	}
	id := identFromStat(&st)
	if identFromStat(&check) != id {
		return nil, errors.Join(ErrPublishDirectoryChanged, unix.Close(checkFD))
	}
	if err := unix.Close(checkFD); err != nil {
		return nil, fmt.Errorf("close publish directory verification handle: %w", err)
	}
	d := &PublishDir{path: filepath.Clean(canonical), plat: publishDirState{fd: fd, id: id}}
	return d, nil
}

func (d *PublishDir) assessInitialNamespace() {
	d.plat.initialNamespaceTrusted, d.plat.initialNamespaceErr = d.unixParentHasPrivateMutationBoundary(-1)
}

func (d *PublishDir) existsLocked(name string) (bool, error) {
	if d.plat.trusted != nil {
		if err := d.plat.trusted.verify(); err != nil {
			return false, err
		}
	}
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
	if d.plat.trusted != nil {
		if err := d.plat.trusted.verify(); err != nil {
			return false, err
		}
		for _, l := range d.plat.trusted.chain {
			if l.node.id == ancestor.plat.id {
				return true, nil
			}
		}
		return false, nil
	}
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
	if d.plat.trusted != nil {
		return d.trustedWorkspace()
	}
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
		fd, err := publishUnixWorkspaceOpenFn(d.plat.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			// No held identity exists, so deletion by this name cannot be proved safe.
			return nil, errors.Join(fmt.Errorf("open publish workspace: %w", err), fmt.Errorf("workspace cleanup incomplete: created identity unavailable"))
		}
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			return nil, errors.Join(fmt.Errorf("stat publish workspace: %w", err), fmt.Errorf("workspace cleanup incomplete: created identity unavailable"), unix.Close(fd))
		}
		// mkdir/open is not atomic in an untrusted parent. Prove the held
		// object is safe before adopting it or changing permissions. A different
		// UID can rename a trusted user's sibling here but cannot mutate an
		// empty, private, trusted-owner directory after this proof.
		if err := d.validateUnixWorkspace(fd, &st); err != nil {
			return nil, errors.Join(err, fmt.Errorf("workspace cleanup incomplete: acquisition not trusted"), unix.Close(fd))
		}
		w := &PublishWorkspace{owner: d, name: name, plat: publishWorkspaceState{
			fd: fd, id: identFromStat(&st), setOwner: d.plat.setOwner, uid: d.plat.uid, gid: d.plat.gid,
			created: make(map[string]struct{}),
		}}
		if err := unix.Fchmod(fd, 0o700); err != nil {
			return nil, errors.Join(fmt.Errorf("harden publish workspace: %w", err), w.closePlatformLocked(d))
		}
		if d.plat.setOwner && effectiveUID() == 0 {
			if err := publishUnixFchownFn(fd, d.plat.uid, d.plat.gid); err != nil {
				return nil, errors.Join(fmt.Errorf("set publish workspace owner: %w", err), w.closePlatformLocked(d))
			}
		}
		return w, nil
	}
	return nil, fmt.Errorf("create publish workspace: exhausted names")
}

func (d *PublishDir) validateUnixWorkspace(fd int, st *unix.Stat_t) error {
	trustedOwner := st.Uid == 0 || int(st.Uid) == os.Geteuid() || (d.plat.setOwner && int(st.Uid) == d.plat.uid)
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || !trustedOwner || st.Mode&0o7777 != 0o700 {
		return fmt.Errorf("publish workspace acquisition: owner or private permissions unproved")
	}
	safe, err := publishUnixACLBoundaryFn(fd)
	if err != nil || !safe {
		return errors.Join(fmt.Errorf("publish workspace acquisition: private ACL unproved"), err)
	}
	names, err := readUnixDirNames(fd)
	if err != nil || len(names) != 0 {
		return errors.Join(fmt.Errorf("publish workspace acquisition: empty directory unproved"), err)
	}
	return nil
}

func (w *PublishWorkspace) createTempLocked(pattern string) (*os.File, string, error) {
	if w.plat.trusted != nil {
		return w.trustedTemp(pattern)
	}
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
		w.plat.created[name] = struct{}{}
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
	if w.plat.trusted != nil {
		id, ok := w.plat.identities[name]
		if !ok {
			return os.ErrInvalid
		}
		return trustedRemove(w.plat.trusted, name, &id)
	}
	if _, owned := w.plat.created[name]; !owned {
		return fmt.Errorf("remove publish temp: name not created by workspace")
	}
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
	if d.plat.trusted != nil {
		return d.trustedPublish(w, tempName, targetName, warn)
	}
	checkFD, err := unix.Open(d.path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return errors.Join(ErrPublishDirectoryChanged, err)
		}
		return fmt.Errorf("verify publish directory: %w", err)
	}
	var st unix.Stat_t
	statErr := publishUnixVerifyStatFn(checkFD, &st)
	closeErr := publishUnixVerifyCloseFn(checkFD)
	if statErr != nil || identFromStat(&st) != d.plat.id {
		verifyErr := ErrPublishDirectoryChanged
		if statErr != nil {
			verifyErr = errors.Join(verifyErr, fmt.Errorf("stat publish directory verification: %w", statErr))
		}
		if closeErr != nil {
			verifyErr = errors.Join(verifyErr, fmt.Errorf("close publish directory verification: %w", closeErr))
		}
		return verifyErr
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
	if w.plat.trusted != nil {
		return w.closeTrustedWorkspace(owner)
	}
	if w.plat.fd < 0 {
		return nil
	}
	contentErr := w.clearContentsLocked()
	var cleanupErr error
	if owner == nil || owner.plat.fd < 0 {
		cleanupErr = fmt.Errorf("publish workspace cleanup: parent is closed")
	} else if !owner.plat.initialNamespaceTrusted {
		cleanupErr = errors.Join(fmt.Errorf("publish workspace cleanup: parent permits untrusted entry replacement at acquisition"), owner.plat.initialNamespaceErr)
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
			} else if safe, safeErr := owner.unixParentHasPrivateMutationBoundary(w.plat.fd); safeErr != nil {
				cleanupErr = safeErr
			} else if !safe {
				cleanupErr = fmt.Errorf("publish workspace cleanup: parent permits untrusted entry replacement")
			} else if contentErr != nil {
				cleanupErr = fmt.Errorf("publish workspace cleanup: contents were not fully cleaned")
			} else if err := publishUnixCleanupUnlinkFn(owner.plat.fd, name, unix.AT_REMOVEDIR); err != nil {
				cleanupErr = fmt.Errorf("remove publish workspace: %w", err)
			}
		}
	}
	closeErr := publishUnixCleanupCloseFn(w.plat.fd)
	w.plat.fd = -1
	return errors.Join(contentErr, cleanupErr, closeErr)
}

func (d *PublishDir) unixParentHasPrivateMutationBoundary(workspaceFD int) (bool, error) {
	var st unix.Stat_t
	if err := unix.Fstat(d.plat.fd, &st); err != nil {
		return false, fmt.Errorf("stat publish parent permissions: %w", err)
	}
	trustedOwner := func(uid uint32) bool {
		return uid == 0 || int(uid) == effectiveUID() || (d.plat.setOwner && int(uid) == d.plat.uid)
	}
	if !trustedOwner(st.Uid) {
		return false, nil
	}
	if safe, err := publishUnixACLBoundaryFn(d.plat.fd); err != nil || !safe {
		return false, err
	}
	if workspaceFD >= 0 {
		var workspace unix.Stat_t
		if err := unix.Fstat(workspaceFD, &workspace); err != nil {
			return false, fmt.Errorf("stat workspace owner: %w", err)
		}
		if !trustedOwner(workspace.Uid) {
			return false, nil
		}
		// Darwin ACLs can authorize DELETE on the child independently of the parent.
		if safe, err := publishUnixACLBoundaryFn(workspaceFD); err != nil || !safe {
			return false, err
		}
	}
	return st.Mode&unix.S_ISVTX != 0 || st.Mode&0o022 == 0, nil
}

func (w *PublishWorkspace) clearContentsLocked() error {
	var result error
	// Never adopt arbitrary directory entries as cleanup targets, even after
	// successful acquisition. Missing created files were already published or
	// removed; all other failures remain warnings and do not stop cleanup.
	for name := range w.plat.created {
		if err := w.removeLocked(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	list, err := unix.Openat(w.plat.fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("workspace content cleanup incomplete: %w", errors.Join(result, err))
	}
	names, readErr := publishUnixCleanupReadFn(list)
	result = errors.Join(result, readErr, unix.Close(list))
	if len(names) != 0 {
		result = errors.Join(result, fmt.Errorf("workspace contains remaining entries"))
	}
	if result != nil {
		return fmt.Errorf("workspace content cleanup incomplete: %w", result)
	}
	return nil
}

func (d *PublishDir) closePlatformLocked() error {
	if d.plat.trusted != nil {
		d.plat.fd = -1
		return d.plat.trusted.Close()
	}
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
	names, readErr := readUnixDirNames(listFD)
	closeErr := unix.Close(listFD)
	if readErr != nil || closeErr != nil {
		return "", fmt.Errorf("list publish parent: %w", errors.Join(readErr, closeErr))
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
