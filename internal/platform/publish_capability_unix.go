//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func cloneTrustedRoot(ctx context.Context, r *TrustedRoot) (_ *TrustedRoot, err error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if err = r.verify(); err != nil {
		return nil, err
	}
	c := &TrustedRoot{backend: r.backend, policy: r.policy, path: r.path, parentOnly: r.parentOnly, aliases: append([]trustedAlias(nil), r.aliases...)}
	defer func() {
		if err != nil {
			err = errors.Join(err, c.Close())
		}
	}()
	for _, l := range r.chain {
		l.fd, err = r.backend.dup(l.fd)
		if err != nil {
			return nil, err
		}
		c.chain = append(c.chain, l)
	}
	if err = c.verify(); err != nil {
		return nil, err
	}
	return c, nil
}

func (fs *PrivateFS) trustedPublishDir(name string) (*PublishDir, error) {
	r, err := fs.trustedDir(name, false)
	if err != nil {
		return nil, err
	}
	c, err := cloneTrustedRoot(context.Background(), r)
	if err != nil {
		return nil, err
	}
	last := c.chain[len(c.chain)-1]
	return &PublishDir{path: filepath.Join(fs.root, name), plat: publishDirState{trusted: c, fd: last.fd, id: last.node.id, uid: int(c.policy.Owner), gid: int(last.node.gid), setOwner: true, initialNamespaceTrusted: true}}, nil
}

func (d *PublishDir) trustedWorkspace() (_ *PublishWorkspace, err error) {
	for i := 0; i < 100; i++ {
		name, e := randomTempName(".mihari-export-*")
		if e != nil {
			return nil, e
		}
		c, e := cloneTrustedRoot(context.Background(), d.plat.trusted)
		if e != nil {
			return nil, e
		}
		parent := c.chain[len(c.chain)-1].fd
		p := RootPolicy{Owner: c.policy.Owner, Mode: 0700}
		fd, e := c.createDir(context.Background(), parent, name, true, p)
		if e != nil {
			closeErr := c.Close()
			if errors.Is(e, os.ErrExist) && closeErr == nil {
				continue
			}
			return nil, errors.Join(e, closeErr)
		}
		n, e := c.backend.stat(fd)
		if e != nil {
			return nil, errors.Join(e, c.backend.close(fd), c.Close())
		}
		c.chain = append(c.chain, trustedLink{fd: fd, name: name, node: n, application: true})
		c.policy = p
		c.path = filepath.Join(c.path, name)
		if e = c.checkLink(len(c.chain)-1, true); e == nil {
			e = c.verify()
		}
		if e != nil {
			return nil, errors.Join(e, c.Close())
		}
		return &PublishWorkspace{owner: d, name: name, plat: publishWorkspaceState{trusted: c, fd: fd, id: n.id, identities: make(map[string]FileIdentity)}}, nil
	}
	return nil, os.ErrExist
}

func (w *PublishWorkspace) trustedTemp(pattern string) (*os.File, string, error) {
	f, name, err := trustedCreateTemp(w.plat.trusted, pattern)
	if err != nil {
		return nil, "", err
	}
	n, err := w.plat.trusted.backend.stat(int(f.Fd()))
	if err != nil {
		return nil, "", errors.Join(err, f.Close())
	}
	w.plat.identities[name] = FileIdentity{plat: n.id}
	return f, name, nil
}

func (d *PublishDir) trustedPublish(w *PublishWorkspace, tempName, targetName string, warn func(error)) error {
	if w.plat.trusted == nil {
		return os.ErrInvalid
	}
	expected, ok := w.plat.identities[tempName]
	if !ok {
		return os.ErrInvalid
	}
	f, id, err := w.plat.trusted.OpenFile(context.Background(), tempName, 0600)
	if err != nil {
		return err
	}
	// Read-only verification holds no pending data. Publication durability is
	// reported by the explicit sync calls below.
	defer func() { _ = f.Close() }()
	if id != expected {
		return ErrIdentityMismatch
	}
	if err = d.plat.trusted.verify(); err != nil {
		return err
	}
	if err = d.plat.trusted.backend.checkACL(d.plat.fd, true, d.plat.trusted.policy.Owner); err != nil {
		return denied("publication parent ACL", err)
	}
	if err = w.plat.trusted.verify(); err != nil {
		return err
	}
	n, err := w.plat.trusted.checkFile(int(f.Fd()), 0600)
	if err != nil {
		return err
	}
	if err = w.plat.trusted.checkFileName(w.plat.fd, tempName, n); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = renameatBetweenNoReplace(w.plat.fd, tempName, d.plat.fd, targetName); err != nil {
		return err
	}
	// The target is committed. Durability failures remain warnings, preserving
	// PublishDir's no-replace and post-publication result contract.
	if err = publishUnixFsyncFn(w.plat.fd); err != nil {
		warn(err)
	}
	if err = publishUnixFsyncFn(d.plat.fd); err != nil {
		warn(err)
	}
	return nil
}

func (w *PublishWorkspace) closeTrustedWorkspace(owner *PublishDir) error {
	r := w.plat.trusted
	var errs []error
	if err := r.verify(); err != nil {
		errs = append(errs, err)
	} else {
		for name, id := range w.plat.identities {
			if err := trustedRemove(r, name, &id); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
		if owner == nil {
			errs = append(errs, ErrPublishCleanupIncomplete)
		} else if len(errs) == 0 {
			if err := r.verify(); err != nil {
				errs = append(errs, err)
			} else {
				parent := r.chain[len(r.chain)-2].fd
				errs = append(errs, unix.Unlinkat(parent, w.name, unix.AT_REMOVEDIR), r.backend.sync(parent))
			}
		}
	}
	w.plat.fd = -1
	err := errors.Join(errs...)
	if err != nil {
		err = errors.Join(ErrPublishCleanupIncomplete, err)
	}
	return errors.Join(err, r.Close())
}
