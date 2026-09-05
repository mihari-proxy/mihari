//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// ControlCredentialFile is a daemon-only scoped capability for the selected C.
// It cannot be used after its WithCredential callback returns.
type ControlCredentialFile struct {
	capabilityLifetime
	parent, anchor *TrustedRoot
	name           string
	mode           uint32
}

// WithCredential borrows the daemon lease without reacquiring filesystem locks.
func (l BorrowedDaemonLease) WithCredential(ctx context.Context, layout ResolvedLayout, fn func(*ControlCredentialFile) error) error {
	return l.withCredential(ctx, layout, fn, nativeLeaseRoot)
}
func (l BorrowedDaemonLease) withCredential(ctx context.Context, layout ResolvedLayout, fn func(*ControlCredentialFile) error, open leaseRootOpener) (err error) {
	if l.state == nil {
		return os.ErrClosed
	}
	s := l.state
	finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if err = s.validate(layout); err != nil {
		return err
	}
	if fn == nil || !filepath.IsAbs(layout.CredentialPath) || filepath.Clean(layout.CredentialPath) != layout.CredentialPath || !trustedComponent(filepath.Base(layout.CredentialPath)) {
		return os.ErrInvalid
	}
	parentPath := filepath.Dir(layout.CredentialPath)
	var parent *TrustedRoot
	for _, root := range s.roots {
		if root.path == parentPath {
			parent = root
			break
		}
	}
	if parent == nil {
		parent, err = open(ctx, parentPath, RootPolicy{Owner: s.owner, Mode: 0755}, true)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, parent.Close()) }()
	}
	if s.dataAnchor == nil {
		return os.ErrClosed
	}
	if err = verifyEndpointMountBoundary(s.dataAnchor, parent); err != nil {
		return err
	}
	mode := uint32(0600)
	if layout.Mode == SystemMode {
		mode = 0644
	}
	c := &ControlCredentialFile{parent: parent, anchor: s.dataAnchor, name: filepath.Base(layout.CredentialPath), mode: mode}
	// Invalidate and join any in-flight use before an external parent is closed.
	defer func() { err = errors.Join(err, c.closeWith(func() error { return nil })) }()
	err = fn(c)
	closeErr := c.closeWith(func() error { return nil })
	return errors.Join(err, closeErr, s.validate(layout), parent.verify(), verifyEndpointMountBoundary(s.dataAnchor, parent), ctx.Err())
}

// Read returns bounded, verified bytes without creating or repairing C.
func (c *ControlCredentialFile) Read(ctx context.Context) (_ []byte, err error) {
	if c == nil {
		return nil, os.ErrClosed
	}
	finish, err := c.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if err = c.verify(); err != nil {
		return nil, err
	}
	f, _, err := c.parent.OpenFile(ctx, c.name, c.mode)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() != 64 && info.Size() != 65 {
		return nil, ErrControlData
	}
	raw, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != info.Size() {
		return nil, ErrControlData
	}
	n, err := c.parent.checkFile(int(f.Fd()), c.mode)
	if err != nil {
		return nil, err
	}
	parentFD := c.parent.chain[len(c.parent.chain)-1].fd
	if err = c.parent.checkFileName(parentFD, c.name, n); err != nil {
		return nil, err
	}
	if err = c.verify(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

// Create publishes C without replacing any existing inode.
func (c *ControlCredentialFile) Create(ctx context.Context, raw []byte) error {
	if c == nil {
		return os.ErrClosed
	}
	finish, err := c.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if err = c.verify(); err != nil {
		return err
	}
	err = c.parent.WriteFile(ctx, c.name, raw, c.mode, nil)
	return errors.Join(err, c.verify(), ctx.Err())
}

func (c *ControlCredentialFile) verify() error {
	if c.parent == nil || c.anchor == nil {
		return os.ErrClosed
	}
	return errors.Join(c.parent.verify(), verifyEndpointMountBoundary(c.anchor, c.parent))
}
