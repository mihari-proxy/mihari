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
	fstatFn             = unix.Fstat
	fchownatFn          = unix.Fchownat
	renameatNoReplaceFn = renameatNoReplace
)

type privateFSState struct {
	rootFD int
	dirs   map[string]int
	uid    int
	gid    int
}

type fileIdentity struct {
	dev uint64
	ino uint64
}

type dirIdentityState struct {
	fd int
	id fileIdentity
}

func (fs *PrivateFS) openRoot() error {
	path := fs.root
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		if !isUnixNotExist(err) {
			return fmt.Errorf("open private data root: %w", err)
		}
		if effectiveUID() == 0 {
			return fmt.Errorf("private fs: refuse to create data root as root")
		}
		if err := unix.Mkdir(path, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("create private data root: %w", err)
		}
		fd, err = unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open private data root: %w", err)
		}
	}
	var st unix.Stat_t
	if err := fstatFn(fd, &st); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("stat private data root: %w", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = unix.Close(fd)
		return fmt.Errorf("private data root is not a directory")
	}
	if err := unix.Fchmod(fd, 0o700); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("chmod private data root: %w", err)
	}
	fs.plat.rootFD = fd
	fs.plat.dirs = make(map[string]int)
	fs.plat.uid = int(st.Uid)
	fs.plat.gid = int(st.Gid)
	return nil
}

func (fs *PrivateFS) closePlatform() error {
	var errs []error
	fs.dirsMu.Lock()
	for name, fd := range fs.plat.dirs {
		if fd >= 0 {
			errs = append(errs, unix.Close(fd))
		}
		delete(fs.plat.dirs, name)
	}
	fs.dirsMu.Unlock()
	if fs.plat.rootFD >= 0 {
		errs = append(errs, unix.Close(fs.plat.rootFD))
		fs.plat.rootFD = -1
	}
	return errors.Join(errs...)
}

func (fs *PrivateFS) ensureDirLocked(name string) error {
	if err := unix.Mkdirat(fs.plat.rootFD, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("mkdir %s: %w", name, err)
	}
	fd, err := unix.Openat(fs.plat.rootFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", name, err)
	}
	var st unix.Stat_t
	if err := fstatFn(fd, &st); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("stat dir %s: %w", name, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = unix.Close(fd)
		return fmt.Errorf("%s is not a directory", name)
	}
	if err := unix.Fchmod(fd, 0o700); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("chmod dir %s: %w", name, err)
	}
	if err := fs.chownat(fs.plat.rootFD, name); err != nil {
		_ = unix.Close(fd)
		return err
	}
	fs.dirsMu.Lock()
	if old, ok := fs.plat.dirs[name]; ok {
		fs.dirsMu.Unlock()
		_ = unix.Close(fd)
		_ = old
		return nil
	}
	fs.plat.dirs[name] = fd
	fs.dirsMu.Unlock()
	return nil
}

func (fs *PrivateFS) dirFD(name string) (int, error) {
	fs.dirsMu.Lock()
	if fd, ok := fs.plat.dirs[name]; ok {
		fs.dirsMu.Unlock()
		return fd, nil
	}
	fs.dirsMu.Unlock()
	fd, err := unix.Openat(fs.plat.rootFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("open dir %s: %w", name, err)
	}
	var st unix.Stat_t
	if err := fstatFn(fd, &st); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("%s is not a directory", name)
	}
	fs.dirsMu.Lock()
	if old, ok := fs.plat.dirs[name]; ok {
		fs.dirsMu.Unlock()
		_ = unix.Close(fd)
		return old, nil
	}
	fs.plat.dirs[name] = fd
	fs.dirsMu.Unlock()
	return fd, nil
}

func (fs *PrivateFS) openAppendLocked(dir, name string) (*os.File, error) {
	dirfd, err := fs.dirFD(dir)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Openat(dirfd, name, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open append %s: %w", name, err)
	}
	var st unix.Stat_t
	if err := fstatFn(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("clear nonblocking mode for %s: %w", name, err)
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("chmod %s: %w", name, err)
	}
	if err := fs.chownat(dirfd, name); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (fs *PrivateFS) openReadCheckedLocked(dir, name string, expected FileIdentity) (*os.File, error) {
	dirfd, err := fs.dirFD(dir)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open read %s: %w", name, err)
	}
	var st unix.Stat_t
	if err := fstatFn(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	got := FileIdentity{plat: identFromStat(&st)}
	if got != expected {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%w", ErrIdentityMismatch)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (fs *PrivateFS) createTempLocked(dir, pattern string) (*os.File, string, error) {
	dirfd, err := fs.dirFD(dir)
	if err != nil {
		return nil, "", err
	}
	for i := 0; i < 100; i++ {
		name, err := randomTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		fd, err := unix.Openat(dirfd, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if err != nil {
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			return nil, "", fmt.Errorf("create temp %s: %w", name, err)
		}
		if err := unix.Fchmod(fd, 0o600); err != nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(dirfd, name, 0)
			return nil, "", err
		}
		if err := fs.chownat(dirfd, name); err != nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(dirfd, name, 0)
			return nil, "", err
		}
		return os.NewFile(uintptr(fd), name), name, nil
	}
	return nil, "", fmt.Errorf("create temp: exhausted names")
}

func canonicalChildDir(name string) (string, bool) {
	switch name {
	case privateLogDirName:
		return privateLogDirName, true
	case privateExportDirName:
		return privateExportDirName, true
	}
	return "", false
}

func (fs *PrivateFS) readDirLocked(dir string) ([]FileEntry, error) {
	dirfd, err := fs.dirFD(dir)
	if err != nil {
		return nil, err
	}
	listfd, err := unix.Openat(dirfd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open dir listing %s: %w", dir, err)
	}
	defer func() {
		// The listing result has already been read; a close failure cannot be
		// recovered here and is safe to ignore during descriptor cleanup.
		_ = unix.Close(listfd)
	}()
	names, err := readUnixDirNames(listfd)
	if err != nil {
		return nil, err
	}
	entries := make([]FileEntry, 0, len(names))
	for _, name := range names {
		var st unix.Stat_t
		if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, fmt.Errorf("stat %s: %w", name, err)
		}
		entries = append(entries, FileEntry{
			Name:     name,
			Mode:     modeFromStat(&st),
			Identity: FileIdentity{plat: identFromStat(&st)},
		})
	}
	return entries, nil
}

func (fs *PrivateFS) openDirIdentityLocked(name string) (*DirectoryIdentity, error) {
	dirfd, err := fs.dirFD(name)
	if err != nil {
		return nil, err
	}
	dup, err := dupCLOEXEC(dirfd)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := fstatFn(dup, &st); err != nil {
		_ = unix.Close(dup)
		return nil, err
	}
	return &DirectoryIdentity{plat: dirIdentityState{fd: dup, id: identFromStat(&st)}}, nil
}

func (fs *PrivateFS) openPublishDirLocked(name string) (*PublishDir, error) {
	fd, err := fs.dirFD(name)
	if err != nil {
		return nil, err
	}
	dup, err := dupCLOEXEC(fd)
	if err != nil {
		return nil, err
	}
	d, err := publishDirFromFD(dup, filepath.Join(fs.root, name))
	if err != nil {
		_ = unix.Close(dup)
		return nil, err
	}
	d.plat.setOwner = true
	d.plat.uid = fs.plat.uid
	d.plat.gid = fs.plat.gid
	d.plat.initialNamespaceTrusted, _ = d.unixParentHasPrivateMutationBoundary(-1)
	return d, nil
}

func (fs *PrivateFS) renameLocked(dir, oldName, newName string, replace bool) error {
	dirfd, err := fs.dirFD(dir)
	if err != nil {
		return err
	}
	var renameErr error
	if replace {
		renameErr = unix.Renameat(dirfd, oldName, dirfd, newName)
	} else {
		renameErr = renameatNoReplaceFn(dirfd, oldName, newName)
	}
	if renameErr != nil {
		return fmt.Errorf("rename %s: %w", oldName, renameErr)
	}
	return nil
}

func (fs *PrivateFS) removeLocked(dir, name string) error {
	dirfd, err := fs.dirFD(dir)
	if err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return fmt.Errorf("remove %s: symlink is not allowed", name)
	}
	if err := unix.Unlinkat(dirfd, name, 0); err != nil {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	return nil
}

func (fs *PrivateFS) chownat(dirfd int, name string) error {
	if effectiveUID() != 0 {
		return nil
	}
	if err := fchownatFn(dirfd, name, fs.plat.uid, fs.plat.gid, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("chown %s: %w", name, err)
	}
	return nil
}

// Close releases the duplicated directory handle. Repeat calls return nil.
func (d *DirectoryIdentity) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if d.plat.fd >= 0 {
		err := unix.Close(d.plat.fd)
		d.plat.fd = -1
		return err
	}
	return nil
}

func (d *DirectoryIdentity) identity() (FileIdentity, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return FileIdentity{}, errPrivateFSClosed
	}
	return FileIdentity{plat: d.plat.id}, nil
}

func identFromStat(st *unix.Stat_t) fileIdentity {
	return fileIdentity{dev: uint64(st.Dev), ino: uint64(st.Ino)}
}

func modeFromStat(st *unix.Stat_t) os.FileMode {
	return modeFromUnixMode(uint32(st.Mode))
}

func modeFromUnixMode(statMode uint32) os.FileMode {
	mode := os.FileMode(statMode & 0o777)
	switch statMode & unix.S_IFMT {
	case unix.S_IFDIR:
		mode |= os.ModeDir
	case unix.S_IFLNK:
		mode |= os.ModeSymlink
	case unix.S_IFIFO:
		mode |= os.ModeNamedPipe
	case unix.S_IFSOCK:
		mode |= os.ModeSocket
	case unix.S_IFBLK:
		mode |= os.ModeDevice
	case unix.S_IFCHR:
		mode |= os.ModeDevice | os.ModeCharDevice
	case unix.S_IFREG:
	default:
		mode |= os.ModeIrregular
	}
	return mode
}

func dupCLOEXEC(fd int) (int, error) {
	n, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err == nil {
		return n, nil
	}
	dup, err := unix.Dup(fd)
	if err != nil {
		return -1, err
	}
	unix.CloseOnExec(dup)
	return dup, nil
}

func readUnixDirNames(fd int) ([]string, error) {
	buf := make([]byte, 8192)
	var names []string
	for {
		n, err := unix.ReadDirent(fd, buf)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			break
		}
		_, _, names = unix.ParseDirent(buf[:n], -1, names)
	}
	return names, nil
}

func isUnixNotExist(err error) bool {
	return errors.Is(err, unix.ENOENT) || errors.Is(err, os.ErrNotExist)
}
