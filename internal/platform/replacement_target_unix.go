//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openReplacementFile(ctx context.Context, path string) (file *os.File, id string, trusted bool, verify func() error, closeParent func() error, err error) {
	parent, err := openReplacementUnixParent(ctx, filepath.Dir(path))
	if err != nil {
		return nil, "", false, nil, nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, parent.Close())
		}
	}()
	fd := parent.chain[len(parent.chain)-1].fd
	name := filepath.Base(path)
	named, err := parent.backend.statAt(fd, name)
	if err != nil {
		return nil, "", false, nil, nil, err
	}
	opened, err := parent.backend.openFile(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", false, nil, nil, err
	}
	file = os.NewFile(uintptr(opened), path)
	openedFile := file
	defer func() {
		if err != nil {
			err = errors.Join(err, openedFile.Close())
		}
	}()
	held, err := parent.backend.stat(opened)
	if err != nil {
		return nil, "", false, nil, nil, err
	}
	if held != named || held.mode&unix.S_IFMT != unix.S_IFREG {
		return nil, "", false, nil, nil, ErrIdentityMismatch
	}
	before, err := file.Stat()
	if err != nil {
		return nil, "", false, nil, nil, err
	}
	mt, ct := sourceFileTimes(before)
	verify = func() error {
		if err := parent.verify(); err != nil {
			return err
		}
		now, err := parent.backend.statAt(fd, name)
		if err != nil {
			return err
		}
		actual, err := parent.backend.stat(opened)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			return err
		}
		m, c := sourceFileTimes(info)
		if now != held || actual != held || m != mt || c != ct || (trusted && !replacementUnixExecutionTrust(parent, opened, actual, uint32(os.Geteuid()))) {
			return ErrIdentityMismatch
		}
		return nil
	}
	trusted = replacementUnixExecutionTrust(parent, opened, held, uint32(os.Geteuid()))
	return file, FileIdentity{plat: held.id}.Key(), trusted, verify, parent.Close, nil
}

func replacementUnixExecutionTrust(parent *ReadOnlySource, fd int, node trustedNode, uid uint32) bool {
	if node.mode&0111 == 0 || node.mode&06000 != 0 || (node.uid != 0 && node.uid != uid) || node.mode&0022 != 0 {
		return false
	}
	if err := parent.backend.checkACL(fd, false, uid); err != nil {
		return false
	}
	for _, link := range parent.chain {
		current, err := parent.backend.stat(link.fd)
		if err != nil || current.id != link.node.id || (current.uid != 0 && current.uid != uid) || current.mode&0022 != 0 {
			return false
		}
		if err := parent.backend.checkACL(link.fd, false, uid); err != nil {
			return false
		}
	}
	return true
}

// ReadOnlySource normally anchors a named directory. A target directly in the
// filesystem root has no named parent, so retain the existing root capability.
func openReplacementUnixParent(ctx context.Context, path string) (_ *ReadOnlySource, err error) {
	if path != "/" {
		return OpenReadOnlySource(ctx, path)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	backend := nativeTrustedBackend{}
	fd, err := backend.openRoot()
	if err != nil {
		return nil, err
	}
	parent := &ReadOnlySource{backend: backend, path: path, chain: []trustedLink{{fd: fd}}}
	defer func() {
		if err != nil {
			err = errors.Join(err, parent.Close())
		}
	}()
	parent.chain[0].node, err = backend.stat(fd)
	if err != nil {
		return nil, err
	}
	mount, err := trustedMountKey(fd)
	if err != nil {
		return nil, err
	}
	parent.mounts = []string{mount}
	if err = backend.checkFS(fd); err != nil {
		return nil, err
	}
	return parent, nil
}

func validateReplacementParent(ctx context.Context, path string) error {
	parent, err := openReplacementUnixParent(ctx, path)
	if err != nil {
		return err
	}
	return errors.Join(parent.verify(), parent.Close())
}
