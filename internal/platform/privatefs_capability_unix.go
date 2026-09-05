//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// NewPrivateFSFromRoot transfers a verified 0700 application root and all its
// retained ancestry into a logging capability. Failure leaves ownership with
// the caller; success makes the original root closed without releasing its fds.
func NewPrivateFSFromRoot(root *TrustedRoot) (*PrivateFS, error) {
	finish, err := root.begin(context.Background())
	if err != nil {
		return nil, err
	}
	defer finish()
	if root.parentOnly || root.policy.Mode != 0700 {
		return nil, os.ErrPermission
	}
	if err := root.verify(); err != nil {
		return nil, err
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.closed {
		return nil, os.ErrClosed
	}
	moved := &TrustedRoot{backend: root.backend, chain: root.chain, aliases: root.aliases, policy: root.policy, path: root.path}
	root.chain, root.aliases = nil, nil
	root.closed = true
	close(root.closing)
	root.closeDone = make(chan struct{})
	close(root.closeDone)
	last := moved.chain[len(moved.chain)-1]
	return &PrivateFS{root: moved.path, plat: privateFSState{rootFD: last.fd, dirs: make(map[string]int), uid: int(last.node.uid), gid: int(last.node.gid), trusted: moved}}, nil
}

func (fs *PrivateFS) trustedDir(name string, create bool) (*TrustedRoot, error) {
	if err := fs.plat.trusted.verify(); err != nil {
		return nil, err
	}
	if r := fs.plat.trustedDirs[name]; r != nil {
		return r, r.verify()
	}
	r, err := fs.plat.trusted.OpenDir(context.Background(), name, RootPolicy{Owner: uint32(fs.plat.uid), Mode: 0700, AllowCreate: create})
	if err != nil {
		return nil, err
	}
	if fs.plat.trustedDirs == nil {
		fs.plat.trustedDirs = make(map[string]*TrustedRoot)
	}
	fs.plat.trustedDirs[name] = r
	return r, nil
}

func (fs *PrivateFS) trustedOpen(dir, name string, flags int, create bool) (_ *os.File, err error) {
	r, err := fs.trustedDir(dir, false)
	if err != nil {
		return nil, err
	}
	parent := r.chain[len(r.chain)-1].fd
	if err = r.backend.checkACL(parent, true, r.policy.Owner); err != nil {
		return nil, denied("creation parent ACL", err)
	}
	flags |= unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	fd := -1
	if create {
		fd, err = r.backend.openFile(parent, name, flags|unix.O_CREAT|unix.O_EXCL, 0600)
	}
	if !create || errors.Is(err, unix.EEXIST) {
		fd, err = r.backend.openFile(parent, name, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.backend.close(fd))
		}
	}()
	n, err := r.checkFile(fd, 0600)
	if err != nil {
		return nil, err
	}
	if n.mode&07777 != 0600 {
		return nil, denied("private file mode", nil)
	}
	if err = r.checkFileName(parent, name, n); err != nil {
		return nil, err
	}
	if err = r.verify(); err != nil {
		return nil, err
	}
	if err = unix.SetNonblock(fd, false); err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func trustedCreateTemp(r *TrustedRoot, pattern string) (_ *os.File, _ string, err error) {
	if err = r.verify(); err != nil {
		return nil, "", err
	}
	parent := r.chain[len(r.chain)-1].fd
	if err = r.backend.checkACL(parent, true, r.policy.Owner); err != nil {
		return nil, "", denied("creation parent ACL", err)
	}
	for i := 0; i < 100; i++ {
		name, e := randomTempName(pattern)
		if e != nil {
			return nil, "", e
		}
		fd, e := r.backend.openFile(parent, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
		if errors.Is(e, unix.EEXIST) {
			continue
		}
		if e != nil {
			return nil, "", e
		}
		n, e := r.checkFile(fd, 0600)
		if e == nil {
			e = r.checkFileName(parent, name, n)
		}
		if e == nil {
			e = r.verify()
		}
		if e == nil {
			e = unix.SetNonblock(fd, false)
		}
		if e != nil {
			return nil, "", errors.Join(e, r.backend.close(fd))
		} // Never remove an unverified created name.
		return os.NewFile(uintptr(fd), name), name, nil
	}
	return nil, "", os.ErrExist
}

func trustedRemove(r *TrustedRoot, name string, expected *FileIdentity) error {
	f, id, err := r.OpenFile(context.Background(), name, 0600)
	if err != nil {
		return err
	}
	defer f.Close() // Read handle owns no pending data; cleanup close cannot change unlink result.
	if expected != nil && id != *expected {
		return ErrIdentityMismatch
	}
	if err := r.verify(); err != nil {
		return err
	}
	n, err := r.checkFile(int(f.Fd()), 0600)
	if err != nil {
		return err
	}
	parent := r.chain[len(r.chain)-1].fd
	if err := r.checkFileName(parent, name, n); err != nil {
		return err
	}
	if err := unix.Unlinkat(parent, name, 0); err != nil {
		return err
	}
	return r.backend.sync(parent)
}

func (fs *PrivateFS) trustedRename(dir, oldName, newName string, replace bool) error {
	r, err := fs.trustedDir(dir, false)
	if err != nil {
		return err
	}
	f, _, err := r.OpenFile(context.Background(), oldName, 0600)
	if err != nil {
		return err
	}
	// This read-only handle has no pending data; closing it cannot change the
	// namespace operation's result.
	defer func() { _ = f.Close() }()
	parent := r.chain[len(r.chain)-1].fd
	var destination *FileIdentity
	if replace {
		dest, id, e := r.OpenFile(context.Background(), newName, 0600)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e == nil {
			destination = &id
			if e = dest.Close(); e != nil {
				return e
			}
		}
	}
	if err = r.verify(); err != nil {
		return err
	}
	n, err := r.checkFile(int(f.Fd()), 0600)
	if err != nil {
		return err
	}
	if err = r.checkFileName(parent, oldName, n); err != nil {
		return err
	}
	if replace && destination != nil {
		if err = r.checkDestination(parent, newName, 0600, destination); err != nil {
			return err
		}
		err = unix.Renameat(parent, oldName, parent, newName)
	} else {
		err = renameatNoReplace(parent, oldName, newName)
	}
	if err != nil {
		return err
	}
	return r.backend.sync(parent)
}
