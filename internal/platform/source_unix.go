//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// SourceEntry is an untrusted read-only migration observation. It grants no
// authority to publish a file or execute its bytes.
type SourceEntry struct {
	Name, Kind, Identity, Mount, SHA256 string
	Size, Mtime, Ctime                  int64
	Nlink                               uint64
}

// ReadOnlySource retains the source namespace and offers no mutation methods.
// Unlike TrustedRoot it permits user-owned ancestors: source bytes must still
// pass the installer's typed validation and independent executable verification.
type ReadOnlySource struct {
	mu      sync.Mutex
	closed  bool
	active  sync.WaitGroup
	chain   []trustedLink
	aliases []trustedAlias
	mounts  []string
	backend trustedBackend
	path    string
}

// OpenReadOnlySource anchors an existing local source without following links.
// Mount transitions are allowed before the anchor; descendants cannot cross one.
func OpenReadOnlySource(ctx context.Context, path string) (_ *ReadOnlySource, err error) {
	if !strings.HasPrefix(path, "/") || path == "/" {
		return nil, os.ErrInvalid
	}
	parts := strings.Split(path[1:], "/")
	for _, name := range parts {
		if !trustedComponent(name) {
			return nil, os.ErrInvalid
		}
	}
	r := &ReadOnlySource{backend: nativeTrustedBackend{}, path: path}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.Close())
		}
	}()
	fd, err := r.backend.openRoot()
	if err != nil {
		return nil, err
	}
	r.chain = append(r.chain, trustedLink{fd: fd})
	r.chain[0].node, err = r.backend.stat(fd)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(parts); i++ {
		name := parts[i]
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		parent := fd
		fd, err = r.backend.openDir(fd, name, false)
		if err != nil && i == 0 && len(r.aliases) == 0 && errors.Is(err, ErrUnsafeComponent) {
			node, statErr := r.backend.statAt(parent, name)
			if statErr != nil {
				return nil, statErr
			}
			if node.uid != 0 || node.links != 1 || node.mode&unix.S_IFMT != unix.S_IFLNK {
				return nil, ErrUnsafeComponent
			}
			target, aliasErr := r.backend.osAlias(parent, name)
			if aliasErr != nil {
				return nil, aliasErr
			}
			r.aliases = append(r.aliases, trustedAlias{name: name, target: target, node: node})
			parts = append([]string{"private", name}, parts[1:]...)
			i--
			fd = parent
			continue
		}
		if err != nil {
			return nil, err
		}
		r.chain = append(r.chain, trustedLink{fd: fd, name: name})
		r.chain[len(r.chain)-1].node, err = r.backend.stat(fd)
		if err != nil {
			return nil, err
		}
	}
	if err = r.backend.checkFS(fd); err != nil {
		return nil, err
	}
	for _, link := range r.chain {
		mount, e := trustedMountKey(link.fd)
		if e != nil {
			return nil, e
		}
		r.mounts = append(r.mounts, mount)
	}
	if err = r.verify(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *ReadOnlySource) begin(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, os.ErrClosed
	}
	r.active.Add(1)
	return r.active.Done, nil
}
func (r *ReadOnlySource) verify() error {
	for _, alias := range r.aliases {
		node, err := r.backend.statAt(r.chain[0].fd, alias.name)
		if err != nil {
			return err
		}
		target, err := r.backend.osAlias(r.chain[0].fd, alias.name)
		if err != nil {
			return err
		}
		if node != alias.node || target != alias.target {
			return ErrIdentityMismatch
		}
	}
	for i, l := range r.chain {
		actual, err := r.backend.stat(l.fd)
		if err != nil {
			return err
		}
		if actual.id != l.node.id || actual.mode&unix.S_IFMT != unix.S_IFDIR {
			return ErrIdentityMismatch
		}
		mount, err := trustedMountKey(l.fd)
		if err != nil {
			return err
		}
		if mount != r.mounts[i] {
			return ErrIdentityMismatch
		}
		if i > 0 {
			named, err := r.backend.statAt(r.chain[i-1].fd, l.name)
			if err != nil {
				return err
			}
			current, openErr := r.backend.openDir(r.chain[i-1].fd, l.name, false)
			if openErr != nil {
				return openErr
			}
			currentMount, mountErr := trustedMountKey(current)
			mountErr = errors.Join(mountErr, r.backend.close(current))
			if mountErr != nil {
				return mountErr
			}
			if currentMount != mount {
				return ErrIdentityMismatch
			}
			if named.id.dev != l.node.id.dev || named.id.ino != l.node.id.ino || named.mode&unix.S_IFMT != unix.S_IFDIR {
				return ErrIdentityMismatch
			}
		}
	}
	return nil
}

// Snapshot returns the retained source identity after namespace revalidation.
func (r *ReadOnlySource) Snapshot(ctx context.Context) (string, string, error) {
	done, err := r.begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer done()
	if err := r.verify(); err != nil {
		return "", "", err
	}
	n := r.chain[len(r.chain)-1].node
	mount, err := trustedMountKey(r.chain[len(r.chain)-1].fd)
	if err != nil {
		return "", "", err
	}
	return r.path, fmt.Sprintf("%x:%x@%s", n.id.dev, n.id.ino, mount), nil
}

// OverlapsTrustedRoot compares the retained source and target ancestry after
// revalidating both namespaces. Device/inode identity deliberately ignores mount
// IDs, so alternate bind-mount names cannot hide a shared source/target tree.
func (r *ReadOnlySource) OverlapsTrustedRoot(ctx context.Context, target *TrustedRoot) (bool, error) {
	return r.compareTrustedRoot(ctx, target, true)
}

// ContainsTrustedRoot checks whether target is the source itself or below it.
// This allows checking an absent migration target's retained creation parent
// without rejecting an unrelated source below that same parent.
func (r *ReadOnlySource) ContainsTrustedRoot(ctx context.Context, target *TrustedRoot) (bool, error) {
	return r.compareTrustedRoot(ctx, target, false)
}

func (r *ReadOnlySource) compareTrustedRoot(ctx context.Context, target *TrustedRoot, checkTargetContainsSource bool) (bool, error) {
	if r == nil || target == nil {
		return false, os.ErrClosed
	}
	done, err := r.begin(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	finish, err := target.begin(ctx)
	if err != nil {
		return false, err
	}
	defer finish()
	if err := r.verify(); err != nil {
		return false, err
	}
	if err := target.verify(); err != nil {
		return false, err
	}
	sourceID := r.chain[len(r.chain)-1].node.id
	targetID := target.chain[len(target.chain)-1].node.id
	if checkTargetContainsSource {
		for _, link := range r.chain {
			if link.node.id == targetID {
				return true, nil
			}
		}
	}
	for _, link := range target.chain {
		if link.node.id == sourceID {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func (r *ReadOnlySource) directory(ctx context.Context, rel string) (_ int, _ func() error, err error) {
	if err := r.verify(); err != nil {
		return -1, nil, err
	}
	fd, err := r.backend.dup(r.chain[len(r.chain)-1].fd)
	if err != nil {
		return -1, nil, err
	}
	var chain []trustedLink
	node, err := r.backend.stat(fd)
	if err != nil {
		return -1, nil, errors.Join(err, r.backend.close(fd))
	}
	chain = append(chain, trustedLink{fd: fd, node: node})
	closeDir := func() error {
		var result error
		for i := len(chain) - 1; i >= 0; i-- {
			link := chain[i]
			if i > 0 {
				named, e := r.backend.statAt(chain[i-1].fd, link.name)
				result = errors.Join(result, e)
				if e == nil && (named.id.dev != link.node.id.dev || named.id.ino != link.node.id.ino || named.mode&unix.S_IFMT != unix.S_IFDIR) {
					result = errors.Join(result, ErrIdentityMismatch)
				}
				current, e := r.backend.openDir(chain[i-1].fd, link.name, true)
				if e != nil {
					result = errors.Join(result, e)
				} else {
					heldMount, heldErr := trustedMountKey(link.fd)
					currentMount, currentErr := trustedMountKey(current)
					result = errors.Join(result, heldErr, currentErr, r.backend.close(current))
					if heldMount != currentMount {
						result = errors.Join(result, ErrIdentityMismatch)
					}
				}
			}
			result = errors.Join(result, r.backend.close(link.fd))
		}
		return errors.Join(result, r.verify())
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, closeDir())
		}
	}()
	if rel != "" && rel != "." {
		for _, name := range strings.Split(rel, "/") {
			if !trustedComponent(name) {
				return -1, nil, os.ErrInvalid
			}
			if err := ctx.Err(); err != nil {
				return -1, nil, err
			}
			child, err := r.backend.openDir(fd, name, true)
			if err != nil {
				return -1, nil, err
			}
			chain = append(chain, trustedLink{fd: child, name: name})
			node, err := r.backend.stat(child)
			if err != nil {
				return -1, nil, err
			}
			chain[len(chain)-1].node = node
			fd = child
		}
	}
	return fd, closeDir, nil
}

// ListNames lists bounded names beneath the anchor; entries are still untrusted.
func (r *ReadOnlySource) ListNames(ctx context.Context, rel string) (names []string, err error) {
	done, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	fd, closeDir, err := r.directory(ctx, rel)
	if err != nil {
		return nil, err
	}
	// Reopening dot gives an independent directory offset rather than dup's shared one.
	defer func() { err = errors.Join(err, closeDir()) }()
	listFD, err := r.backend.openDir(fd, ".", true)
	if err != nil {
		if listFD >= 0 {
			err = errors.Join(err, r.backend.close(listFD))
		}
		return nil, err
	}
	f := os.NewFile(uintptr(listFD), rel)
	defer func() { err = errors.Join(err, f.Close()) }()
	names, err = f.Readdirnames(10001)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if len(names) > 10000 {
		return nil, os.ErrInvalid
	}
	return names, errors.Join(err, r.verify())
}

// Read observes one regular, single-link file and reads it with a strict bound.
// Metadata is checked again after IO so concurrent rewrites cannot become a snapshot.
func (r *ReadOnlySource) Read(ctx context.Context, rel string, limit int64) (entry SourceEntry, raw []byte, err error) {
	return r.read(ctx, rel, limit, true)
}

// Stat observes metadata and hashes a bounded file without retaining its bytes.
// It applies the same namespace, identity and mutation checks as Read.
func (r *ReadOnlySource) Stat(ctx context.Context, rel string, limit int64) (SourceEntry, error) {
	entry, _, err := r.read(ctx, rel, limit, false)
	return entry, err
}

func (r *ReadOnlySource) read(ctx context.Context, rel string, limit int64, retain bool) (entry SourceEntry, raw []byte, err error) {
	done, err := r.begin(ctx)
	if err != nil {
		return entry, nil, err
	}
	defer done()
	at := strings.LastIndexByte(rel, '/')
	dirRel := "."
	name := rel
	if at >= 0 {
		dirRel, name = rel[:at], rel[at+1:]
	}
	if !trustedComponent(name) || limit <= 0 {
		return entry, nil, os.ErrInvalid
	}
	fd, closeDir, err := r.directory(ctx, dirRel)
	if err != nil {
		return entry, nil, err
	}
	defer func() { err = errors.Join(err, closeDir()) }()
	named, err := r.backend.statAt(fd, name)
	if err != nil {
		return entry, nil, err
	}
	entry = SourceEntry{Name: name, Nlink: named.links, Identity: FileIdentity{plat: named.id}.Key()}
	switch named.mode & unix.S_IFMT {
	case unix.S_IFDIR:
		child, e := r.backend.openDir(fd, name, true)
		if e != nil {
			return entry, nil, e
		}
		mount, e := trustedMountKey(child)
		e = errors.Join(e, r.backend.close(child))
		if e != nil {
			return entry, nil, e
		}
		entry.Kind = "dir"
		entry.Mount = mount
		return entry, nil, r.verify()
	case unix.S_IFLNK:
		entry.Kind = "symlink"
		return entry, nil, ErrUnsafeComponent
	case unix.S_IFSOCK:
		entry.Kind = "socket"
		return entry, nil, nil
	case unix.S_IFIFO:
		entry.Kind = "fifo"
		return entry, nil, nil
	case unix.S_IFREG:
	default:
		return entry, nil, ErrUnsafeComponent
	}
	if named.links != 1 {
		return entry, nil, ErrUnsafeComponent
	}
	fileFD, err := r.backend.openFile(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return entry, nil, err
	}
	f := os.NewFile(uintptr(fileFD), name)
	defer func() { err = errors.Join(err, f.Close()) }()
	before, err := f.Stat()
	if err != nil {
		return entry, nil, err
	}
	held, err := r.backend.stat(fileFD)
	if err != nil {
		return entry, nil, err
	}
	if held.id.dev != named.id.dev || held.id.ino != named.id.ino || held.links != 1 || held.mode&unix.S_IFMT != unix.S_IFREG {
		return entry, nil, ErrIdentityMismatch
	}
	entry.Kind = "file"
	entry.Size = before.Size()
	entry.Mount, err = trustedMountKey(fileFD)
	if err != nil {
		return entry, nil, err
	}
	entry.Mtime, entry.Ctime = sourceFileTimes(before)
	if entry.Size > limit {
		return entry, nil, os.ErrInvalid
	}
	raw, size, hash, err := readSourceContent(ctx, f, limit, retain)
	if size > limit {
		entry.Size = size
		return entry, nil, errors.Join(os.ErrInvalid, err)
	}
	if err != nil {
		return entry, nil, err
	}
	if size != entry.Size {
		return entry, nil, ErrIdentityMismatch
	}
	after, err := f.Stat()
	if err != nil {
		return entry, nil, err
	}
	mt, ct := sourceFileTimes(after)
	now, err := r.backend.statAt(fd, name)
	if err != nil {
		return entry, nil, err
	}
	if now != named || after.Size() != entry.Size || mt != entry.Mtime || ct != entry.Ctime {
		return entry, nil, ErrIdentityMismatch
	}
	entry.SHA256 = hash
	return entry, raw, r.verify()
}

// Close waits for owned reads before releasing all retained source descriptors.
func (r *ReadOnlySource) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	r.active.Wait()
	var err error
	for i := len(r.chain) - 1; i >= 0; i-- {
		err = errors.Join(err, r.backend.close(r.chain[i].fd))
	}
	return err
}
