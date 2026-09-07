//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// OpenFile opens one read-only regular file. maxMode is a permission ceiling.
func (r *TrustedRoot) OpenFile(ctx context.Context, name string, maxMode uint32) (_ *os.File, _ FileIdentity, err error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return nil, FileIdentity{}, err
	}
	defer finish()
	if !trustedComponent(name) || !trustedFileMode(maxMode) {
		return nil, FileIdentity{}, os.ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return nil, FileIdentity{}, err
	}
	if err = r.verify(); err != nil {
		return nil, FileIdentity{}, err
	}
	parent := r.chain[len(r.chain)-1].fd
	fd, err := r.backend.openFile(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, FileIdentity{}, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.backend.close(fd))
		}
	}()
	n, err := r.checkFile(fd, maxMode)
	if err != nil {
		return nil, FileIdentity{}, err
	}
	if err = r.checkFileName(parent, name, n); err != nil {
		return nil, FileIdentity{}, err
	}
	if err = unix.SetNonblock(fd, false); err != nil {
		return nil, FileIdentity{}, err
	}
	if err = r.verify(); err != nil {
		return nil, FileIdentity{}, err
	}
	return os.NewFile(uintptr(fd), name), FileIdentity{plat: n.id}, nil
}

// WriteFile atomically publishes bytes through an exclusive 0600 temporary
// inode. A nil expected identity requests no-replace publication; a non-nil
// identity authorizes replacement of exactly that previously observed inode.
func (r *TrustedRoot) WriteFile(ctx context.Context, name string, data []byte, mode uint32, expected *FileIdentity) (err error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if !trustedComponent(name) || !trustedFileMode(mode) {
		return os.ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = r.verify(); err != nil {
		return err
	}
	parent := r.chain[len(r.chain)-1].fd
	if err = r.backend.checkACL(parent, true, r.policy.Owner); err != nil {
		return denied("creation parent ACL", err)
	}
	if err = r.checkDestination(parent, name, mode, expected); err != nil {
		return err
	}
	temp, err := randomTempName(".mihari-*")
	if err != nil {
		return err
	}
	fd, err := r.backend.openFile(parent, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), temp)
	defer func() { err = errors.Join(err, f.Close()) }()
	initial, err := r.backend.stat(fd)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if published {
			return
		}
		// A name changed by another actor is never removed during cleanup.
		if e := r.verify(); e != nil {
			err = errors.Join(err, e)
			return
		}
		n, e := r.backend.statAt(parent, temp)
		if e != nil {
			err = errors.Join(err, e)
			return
		}
		if n.id != initial.id {
			err = errors.Join(err, ErrIdentityMismatch)
			return
		}
		err = errors.Join(err, unix.Unlinkat(parent, temp, 0), r.backend.sync(parent))
	}()
	n, err := r.checkFile(fd, 0600)
	if err != nil {
		return err
	}
	if err = r.checkFileName(parent, temp, n); err != nil {
		return err
	}
	if err = unix.SetNonblock(fd, false); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if written, e := f.Write(data); e != nil {
		return e
	} else if written != len(data) {
		return io.ErrShortWrite
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if _, err = r.checkFile(fd, 0600); err != nil {
		return err
	}
	if err = unix.Fchmod(fd, mode); err != nil {
		return err
	}
	n, err = r.checkFile(fd, mode)
	if err != nil {
		return err
	}
	if n.mode&07777 != mode {
		return denied("published file mode", nil)
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = r.verify(); err != nil {
		return err
	}
	if err = r.backend.checkACL(parent, true, r.policy.Owner); err != nil {
		return denied("publication parent ACL", err)
	}
	if err = r.checkFileName(parent, temp, n); err != nil {
		return err
	}
	if err = r.checkDestination(parent, name, mode, expected); err != nil {
		return err
	}
	if expected == nil {
		err = renameatNoReplace(parent, temp, name)
	} else {
		err = unix.Renameat(parent, temp, parent, name)
	}
	if err != nil {
		return err
	}
	published = true
	// A sync failure here means publication happened but durability is unproved.
	return r.backend.sync(parent)
}

func trustedFileMode(mode uint32) bool {
	return mode == 0600 || mode == 0644 || mode == 0700 || mode == 0755
}
func (r *TrustedRoot) checkFile(fd int, maxMode uint32) (trustedNode, error) {
	n, err := r.backend.stat(fd)
	if err != nil {
		return n, err
	}
	if n.mode&unix.S_IFMT != unix.S_IFREG || n.links != 1 {
		return n, ErrUnsafeComponent
	}
	if n.uid != r.policy.Owner || n.mode&07777&^maxMode != 0 {
		return n, denied("regular file owner or permissions", nil)
	}
	if err = r.backend.checkFS(fd); err != nil {
		return n, denied("regular file filesystem", err)
	}
	if err = r.backend.checkACL(fd, true, r.policy.Owner); err != nil {
		return n, denied("regular file ACL", err)
	}
	return n, nil
}
func (r *TrustedRoot) checkFileName(parent int, name string, expected trustedNode) error {
	n, err := r.backend.statAt(parent, name)
	if err != nil {
		return err
	}
	if n.mode&unix.S_IFMT != unix.S_IFREG || n.links != 1 {
		return ErrUnsafeComponent
	}
	if n != expected {
		return denied("regular file name changed", ErrIdentityMismatch)
	}
	return nil
}
func (r *TrustedRoot) checkDestination(parent int, name string, mode uint32, expected *FileIdentity) error {
	if expected == nil {
		_, err := r.backend.statAt(parent, name)
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		if err != nil {
			return err
		}
		return os.ErrExist
	}
	fd, err := r.backend.openFile(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	n, checkErr := r.checkFile(fd, mode)
	if checkErr == nil && n.id != expected.plat {
		checkErr = denied("replacement identity changed", ErrIdentityMismatch)
	}
	if checkErr == nil {
		checkErr = r.checkFileName(parent, name, n)
	}
	return errors.Join(checkErr, r.backend.close(fd))
}

// Snapshot returns freshly verified display/identity metadata, not a writable
// descriptor or authority setter. Ownership stays with the root capability.
func (r *TrustedRoot) Snapshot(ctx context.Context) (path, identity string, owner, mode uint32, err error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return "", "", 0, 0, err
	}
	defer finish()
	if err = r.verify(); err != nil {
		return "", "", 0, 0, err
	}
	n := r.chain[len(r.chain)-1].node
	mount, e := trustedMountKey(r.chain[len(r.chain)-1].fd)
	if e != nil {
		return "", "", 0, 0, e
	}
	return r.path, fmt.Sprintf("%x:%x@%s", n.id.dev, n.id.ino, mount), n.uid, n.mode & 07777, nil
}

// Key returns an opaque observation suitable for a same-boot journal identity.
// It cannot be parsed back into a FileIdentity authorization.
func (id FileIdentity) Key() string { return fmt.Sprintf("%x:%x", id.plat.dev, id.plat.ino) }

// Sync makes the held directory durable after checking its namespace.
func (r *TrustedRoot) Sync(ctx context.Context) error {
	finish, e := r.begin(ctx)
	if e != nil {
		return e
	}
	defer finish()
	if e = r.verify(); e != nil {
		return e
	}
	return r.backend.sync(r.chain[len(r.chain)-1].fd)
}

// RemoveFile unlinks only the exact previously opened regular inode.
func (r *TrustedRoot) RemoveFile(ctx context.Context, name string, mode uint32, expected FileIdentity) error {
	finish, e := r.begin(ctx)
	if e != nil {
		return e
	}
	defer finish()
	if !trustedComponent(name) || !trustedFileMode(mode) {
		return os.ErrInvalid
	}
	if e = r.verify(); e != nil {
		return e
	}
	parent := r.chain[len(r.chain)-1].fd
	if e = r.backend.checkACL(parent, true, r.policy.Owner); e != nil {
		return e
	}
	if e = r.checkDestination(parent, name, mode, &expected); e != nil {
		return e
	}
	if e = unix.Unlinkat(parent, name, 0); e != nil {
		return e
	}
	return r.backend.sync(parent)
}

// MoveFileTo publishes a held source inode into a held directory, including a
// same-parent backup using the same capability.
// Both roots remain owned by the caller; expected=nil requires absence. Callers
// serialize cross-directory moves under their lifecycle/transaction lease.
func (r *TrustedRoot) MoveFileTo(ctx context.Context, name string, id FileIdentity, to *TrustedRoot, target string, mode uint32, expected *FileIdentity) error {
	_, err := r.MoveFileToPublished(ctx, name, id, to, target, mode, expected)
	return err
}

// MoveFileToPublished reports the single rename commit even if parent sync fails.
func (r *TrustedRoot) MoveFileToPublished(ctx context.Context, name string, id FileIdentity, to *TrustedRoot, target string, mode uint32, expected *FileIdentity) (published bool, err error) {
	finish, e := r.begin(ctx)
	if e != nil {
		return false, e
	}
	defer finish()
	if r != to {
		finishTo, err := to.begin(ctx)
		if err != nil {
			return false, err
		}
		defer finishTo()
	}
	if !trustedComponent(name) || !trustedComponent(target) || !trustedFileMode(mode) || r.policy.Owner != to.policy.Owner {
		return false, os.ErrInvalid
	}
	if e = r.verify(); e != nil {
		return false, e
	}
	if e = to.verify(); e != nil {
		return false, e
	}
	sourceParent := r.chain[len(r.chain)-1].fd
	targetParent := to.chain[len(to.chain)-1].fd
	if e = r.backend.checkACL(sourceParent, true, r.policy.Owner); e != nil {
		return false, e
	}
	if e = to.backend.checkACL(targetParent, true, to.policy.Owner); e != nil {
		return false, e
	}
	if e = r.checkDestination(sourceParent, name, mode, &id); e != nil {
		return false, e
	}
	if e = to.checkDestination(targetParent, target, mode, expected); e != nil {
		return false, e
	}
	fd, e := r.backend.openFile(sourceParent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return false, e
	}
	defer func() { _ = r.backend.close(fd) }() // Read-only source handle; both directory sync results are reported below.
	n, e := r.checkFile(fd, mode)
	if e != nil {
		return false, e
	}
	if n.id != id.plat {
		return false, ErrIdentityMismatch
	}
	if n.mode&07777 != mode {
		return false, os.ErrPermission
	}
	n, e = r.checkFile(fd, mode)
	if e != nil {
		return false, e
	}
	if e = r.checkFileName(sourceParent, name, n); e != nil {
		return false, e
	}
	if expected == nil {
		e = renameatBetweenNoReplace(sourceParent, name, targetParent, target)
	} else {
		e = unix.Renameat(sourceParent, name, targetParent, target)
	}
	if e != nil {
		return false, e
	}
	return true, errors.Join(r.backend.sync(sourceParent), to.backend.sync(targetParent))
}
