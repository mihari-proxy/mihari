//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strings"
)

// ServiceEntry is an opaque retained observation for the narrow Unix service
// definition symlink exception. Its identity cannot be constructed by callers.
type ServiceEntry struct {
	Present     bool
	Link        string
	Bytes       []byte
	Mode, Owner uint32
	node        trustedNode
}

// ReadServiceEntry reads a unit/plist or a root-owned service link without
// following the link. The trusted parent remains owned by the caller.
func (r *TrustedRoot) ReadServiceEntry(ctx context.Context, name string) (out ServiceEntry, err error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return out, err
	}
	defer finish()
	if !trustedComponent(name) {
		return out, os.ErrInvalid
	}
	if err = r.verify(); err != nil {
		return out, err
	}
	parent := r.chain[len(r.chain)-1].fd
	n, err := r.backend.statAt(parent, name)
	if errors.Is(err, unix.ENOENT) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if n.uid != r.policy.Owner {
		return out, os.ErrPermission
	}
	out = ServiceEntry{Present: true, Mode: n.mode & 07777, Owner: n.uid, node: n}
	if n.mode&unix.S_IFMT == unix.S_IFLNK {
		if n.links != 1 {
			return ServiceEntry{}, ErrUnsafeComponent
		}
		buffer := make([]byte, 4096)
		size, e := unix.Readlinkat(parent, name, buffer)
		if e != nil {
			return ServiceEntry{}, e
		}
		if size == len(buffer) {
			return ServiceEntry{}, os.ErrInvalid
		}
		out.Link = string(buffer[:size])
	} else {
		fd, e := r.backend.openFile(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		if e != nil {
			return ServiceEntry{}, e
		}
		file := os.NewFile(uintptr(fd), name)
		defer func() { err = errors.Join(err, file.Close()) }()
		checked, e := r.checkFile(fd, 0644)
		if e != nil {
			return ServiceEntry{}, e
		}
		if checked.id != n.id {
			return ServiceEntry{}, ErrIdentityMismatch
		}
		out.Bytes, e = io.ReadAll(io.LimitReader(file, (1<<20)+1))
		if e != nil || len(out.Bytes) > 1<<20 {
			return ServiceEntry{}, os.ErrInvalid
		}
	}
	current, err := r.backend.statAt(parent, name)
	if err != nil {
		return ServiceEntry{}, err
	}
	if current != n {
		return ServiceEntry{}, ErrIdentityMismatch
	}
	return out, r.verify()
}

// Key returns observed identity for a durable backup record, not write authority.
func (s ServiceEntry) Key() string { return FileIdentity{plat: s.node.id}.Key() }

// WriteServiceEntry atomically replaces exactly the observed unit/plist/link.
// Link publication is reserved for service masks and enable links; application
// data writers continue using WriteFile, which rejects symlinks.
func (r *TrustedRoot) WriteServiceEntry(ctx context.Context, name string, body []byte, link string, mode uint32, expected ServiceEntry) (err error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if !trustedComponent(name) || mode&^uint32(0644) != 0 {
		return os.ErrInvalid
	}
	if link != "" && (!strings.HasPrefix(link, "/") || strings.ContainsRune(link, 0) || len(body) != 0) {
		return os.ErrInvalid
	}
	if err = r.verify(); err != nil {
		return err
	}
	parent := r.chain[len(r.chain)-1].fd
	if err = r.backend.checkACL(parent, true, r.policy.Owner); err != nil {
		return err
	}
	if err = r.matchServiceEntry(parent, name, expected); err != nil {
		return err
	}
	tmp, err := randomTempName(".mihari-service-*")
	if err != nil {
		return err
	}
	var created trustedNode
	published := false
	createdOK := false
	defer func() {
		if !published && createdOK {
			current, e := r.backend.statAt(parent, tmp)
			if e == nil && current.id == created.id {
				err = errors.Join(err, unix.Unlinkat(parent, tmp, 0), r.backend.sync(parent))
			} else {
				err = errors.Join(err, e)
			}
		}
	}()
	if link != "" {
		if err = unix.Symlinkat(link, parent, tmp); err != nil {
			return err
		}
		created, err = r.backend.statAt(parent, tmp)
		if err != nil {
			return err
		}
		createdOK = true
	} else {
		fd, e := r.backend.openFile(parent, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if e != nil {
			return e
		}
		file := os.NewFile(uintptr(fd), tmp)
		defer func() { err = errors.Join(err, file.Close()) }()
		created, e = r.backend.stat(fd)
		if e != nil {
			return e
		}
		createdOK = true
		_, e = r.checkFile(fd, 0600)
		if e != nil {
			return e
		}
		if _, err = file.Write(body); err != nil {
			return err
		}
		if err = file.Sync(); err != nil {
			return err
		}
		if err = unix.Fchmod(fd, mode); err != nil {
			return err
		}
		if _, err = r.checkFile(fd, mode); err != nil {
			return err
		}
		if err = file.Sync(); err != nil {
			return err
		}
	}

	if created.uid != r.policy.Owner || created.links != 1 {
		return os.ErrPermission
	}
	if err = r.verify(); err != nil {
		return err
	}
	if err = r.matchServiceEntry(parent, name, expected); err != nil {
		return err
	}
	if expected.Present {
		err = unix.Renameat(parent, tmp, parent, name)
	} else {
		err = renameatNoReplace(parent, tmp, name)
	}
	if err != nil {
		return err
	}
	published = true
	return r.backend.sync(parent)
}

// RemoveServiceEntry removes only an exact held unit/plist/link observation.
func (r *TrustedRoot) RemoveServiceEntry(ctx context.Context, name string, expected ServiceEntry) error {
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if !trustedComponent(name) {
		return os.ErrInvalid
	}
	if err = r.verify(); err != nil {
		return err
	}
	parent := r.chain[len(r.chain)-1].fd
	if err = r.backend.checkACL(parent, true, r.policy.Owner); err != nil {
		return err
	}
	if err = r.matchServiceEntry(parent, name, expected); err != nil {
		return err
	}
	if !expected.Present {
		return nil
	}
	if err = unix.Unlinkat(parent, name, 0); err != nil {
		return err
	}
	return r.backend.sync(parent)
}
func (r *TrustedRoot) matchServiceEntry(parent int, name string, expected ServiceEntry) error {
	n, err := r.backend.statAt(parent, name)
	if !expected.Present && errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if !expected.Present || n != expected.node {
		return ErrIdentityMismatch
	}
	if n.uid != r.policy.Owner || n.links != 1 {
		return os.ErrPermission
	}
	return nil
}

// MoveDirTo atomically publishes a held private staging directory. Destination
// must be absent. Both parent directories are synced and child ownership stays
// with the caller, which must close its old namespace capability after success.
func (r *TrustedRoot) MoveDirTo(ctx context.Context, name string, child *TrustedRoot, to *TrustedRoot, target string) (published bool, err error) {
	if r == nil || child == nil || to == nil || r == child || to == child {
		return false, os.ErrInvalid
	}
	finish, err := r.begin(ctx)
	if err != nil {
		return false, err
	}
	defer finish()
	if r != to {
		done, e := to.begin(ctx)
		if e != nil {
			return false, e
		}
		defer done()
	}
	done, err := child.begin(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	if !trustedComponent(name) || !trustedComponent(target) || r.policy.Owner != to.policy.Owner || child.policy.Owner != r.policy.Owner || child.policy.Mode != 0700 {
		return false, os.ErrInvalid
	}
	if err = r.verify(); err != nil {
		return false, err
	}
	if err = to.verify(); err != nil {
		return false, err
	}
	if err = child.verify(); err != nil {
		return false, err
	}
	fromFD, toFD := r.chain[len(r.chain)-1].fd, to.chain[len(to.chain)-1].fd
	if err = r.backend.checkACL(fromFD, true, r.policy.Owner); err != nil {
		return false, err
	}
	if err = to.backend.checkACL(toFD, true, to.policy.Owner); err != nil {
		return false, err
	}
	n, err := r.backend.statAt(fromFD, name)
	if err != nil {
		return false, err
	}
	if !sameTrustedDirectory(n, child.chain[len(child.chain)-1].node) {
		return false, ErrIdentityMismatch
	}
	if err = renameatBetweenNoReplace(fromFD, name, toFD, target); err != nil {
		return false, err
	}
	return true, errors.Join(r.backend.sync(fromFD), to.backend.sync(toFD))
}

// MoveServiceEntryTo publishes an already private-staged service object while
// preserving its recorded inode. Both directory capabilities remain caller-owned.
func (r *TrustedRoot) MoveServiceEntryTo(ctx context.Context, name string, candidate ServiceEntry, to *TrustedRoot, target string, expected ServiceEntry) error {
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if r != to {
		done, err := to.begin(ctx)
		if err != nil {
			return err
		}
		defer done()
	}
	if !trustedComponent(name) || !trustedComponent(target) || !candidate.Present || r.policy.Owner != to.policy.Owner {
		return os.ErrInvalid
	}
	if err := r.verify(); err != nil {
		return err
	}
	if err := to.verify(); err != nil {
		return err
	}
	fromFD, toFD := r.chain[len(r.chain)-1].fd, to.chain[len(to.chain)-1].fd
	if err := r.backend.checkACL(fromFD, true, r.policy.Owner); err != nil {
		return err
	}
	if err := to.backend.checkACL(toFD, true, to.policy.Owner); err != nil {
		return err
	}
	if err := r.matchServiceEntry(fromFD, name, candidate); err != nil {
		return err
	}
	if err := to.matchServiceEntry(toFD, target, expected); err != nil {
		return err
	}
	if expected.Present {
		err = unix.Renameat(fromFD, name, toFD, target)
	} else {
		err = renameatBetweenNoReplace(fromFD, name, toFD, target)
	}
	if err != nil {
		return err
	}
	return errors.Join(r.backend.sync(fromFD), to.backend.sync(toFD))
}

// ExchangeServiceEntryWith swaps two verified service objects atomically, keeping
// both recorded inodes available for a later recovery. Published remains true
// if either parent synchronization fails after the exchange.
func (r *TrustedRoot) ExchangeServiceEntryWith(ctx context.Context, name string, candidate ServiceEntry, to *TrustedRoot, target string, expected ServiceEntry) (published bool, err error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return false, err
	}
	defer finish()
	if r != to {
		done, err := to.begin(ctx)
		if err != nil {
			return false, err
		}
		defer done()
	}
	if !trustedComponent(name) || !trustedComponent(target) || !candidate.Present || !expected.Present || r.policy.Owner != to.policy.Owner {
		return false, os.ErrInvalid
	}
	if err := r.verify(); err != nil {
		return false, err
	}
	if err := to.verify(); err != nil {
		return false, err
	}
	fromFD, toFD := r.chain[len(r.chain)-1].fd, to.chain[len(to.chain)-1].fd
	if err := r.backend.checkACL(fromFD, true, r.policy.Owner); err != nil {
		return false, err
	}
	if err := to.backend.checkACL(toFD, true, to.policy.Owner); err != nil {
		return false, err
	}
	if err := r.matchServiceEntry(fromFD, name, candidate); err != nil {
		return false, err
	}
	if err := to.matchServiceEntry(toFD, target, expected); err != nil {
		return false, err
	}
	if err := renameatExchange(fromFD, name, toFD, target); err != nil {
		return false, err
	}
	return true, errors.Join(r.backend.sync(fromFD), to.backend.sync(toFD))
}
