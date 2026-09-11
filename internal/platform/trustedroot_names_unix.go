//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"

	"golang.org/x/sys/unix"
)

// ReadNames lists at most 4096 immediate names through the held directory.
// Names are observations only; callers must open children through this root.
func (r *TrustedRoot) ReadNames(ctx context.Context) (names []string, err error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if err = r.verify(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(r.chain[len(r.chain)-1].fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "trusted-directory")
	defer func() { err = errors.Join(err, file.Close()) }()
	names, err = file.Readdirnames(4097)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if len(names) > 4096 {
		return nil, os.ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = r.verify(); err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// RemoveEmptyDir removes only a freshly verified, empty private child directory.
func (r *TrustedRoot) RemoveEmptyDir(ctx context.Context, name string) (err error) {
	if r == nil {
		return os.ErrInvalid
	}
	child, err := r.OpenDir(ctx, name, RootPolicy{Owner: r.policy.Owner, Mode: 0700})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, child.Close()) }()
	names, err := child.ReadNames(ctx)
	if err != nil {
		return err
	}
	if len(names) != 0 {
		return unix.ENOTEMPTY
	}
	finish, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if err = r.verify(); err != nil {
		return err
	}
	if err = child.verify(); err != nil {
		return err
	}
	fd := r.chain[len(r.chain)-1].fd
	named, err := r.backend.statAt(fd, name)
	if err != nil {
		return err
	}
	if !sameTrustedDirectory(named, child.chain[len(child.chain)-1].node) {
		return ErrIdentityMismatch
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = unix.Unlinkat(fd, name, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	return r.backend.sync(fd)
}
