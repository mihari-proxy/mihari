//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// RootPolicy specifies the owner and exact permissions of an application directory.
// AllowCreate permits creation of only the final component.
type RootPolicy struct {
	Owner       uint32
	Mode        uint32
	AllowCreate bool
}

type trustedNode struct {
	id             fileIdentity
	uid, gid, mode uint32
	links          uint64
}
type trustedLink struct {
	fd          int
	name        string
	node        trustedNode
	application bool
}
type trustedBackend interface {
	openRoot() (int, error)
	openDir(int, string, bool) (int, error)
	openFile(int, string, int, uint32) (int, error)
	stat(int) (trustedNode, error)
	statAt(int, string) (trustedNode, error)
	checkFS(int) error
	checkACL(int, bool, uint32) error
	close(int) error
	dup(int) (int, error)
	mkdir(int, string, uint32) error
	prepareDir(int, uint32, uint32) error
	sync(int) error
	osAlias(int, string) (string, error)
}

type trustedAlias struct {
	name, target string
	node         trustedNode
}

// TrustedRoot owns a directory descriptor and its verified namespace ancestry.
// A zero value is closed and cannot authorize IO. Do not copy after first use.
type TrustedRoot struct {
	mu        sync.Mutex
	backend   trustedBackend
	chain     []trustedLink
	aliases   []trustedAlias
	policy    RootPolicy
	path      string // display/path matching only; never reopened for IO
	closed    bool
	operation chan struct{}
	closing   chan struct{}
	closeDone chan struct{}
	closeErr  error
}

// OpenTrustedRoot opens a directory without following application symlinks.
func OpenTrustedRoot(ctx context.Context, path string, policy RootPolicy) (*TrustedRoot, error) {
	return openTrustedRoot(ctx, path, policy, nativeTrustedBackend{})
}

// OpenDir acquires one child beneath this anchor, rejecting nested mounts.
// The child owns independent retained descriptors and has the same owner.
func (r *TrustedRoot) OpenDir(ctx context.Context, name string, p RootPolicy) (_ *TrustedRoot, err error) {
	finish, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if !trustedComponent(name) || !validRootPolicy(p) || p.Owner != r.policy.Owner {
		return nil, os.ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = r.verify(); err != nil {
		return nil, err
	}
	child := &TrustedRoot{backend: r.backend, policy: p, path: r.path + "/" + name, aliases: append([]trustedAlias(nil), r.aliases...)}
	defer func() {
		if err != nil {
			err = errors.Join(err, child.Close())
		}
	}()
	for _, l := range r.chain {
		l.fd, err = r.backend.dup(l.fd)
		if err != nil {
			return nil, err
		}
		child.chain = append(child.chain, l)
	}
	parent := child.chain[len(child.chain)-1].fd
	fd, err := child.backend.openDir(parent, name, true)
	if errors.Is(err, unix.ENOENT) && p.AllowCreate {
		fd, err = child.createDir(ctx, parent, name, true, p)
	}
	if err != nil {
		return nil, err
	}
	child.chain = append(child.chain, trustedLink{fd: fd, name: name, application: true})
	i := len(child.chain) - 1
	child.chain[i].node, err = child.backend.stat(fd)
	if err != nil {
		return nil, err
	}
	if err = child.checkLink(i, true); err != nil {
		return nil, err
	}
	if err = child.verify(); err != nil {
		return nil, err
	}
	return child, nil
}

func openTrustedRoot(ctx context.Context, path string, policy RootPolicy, b trustedBackend) (_ *TrustedRoot, err error) {
	if !strings.HasPrefix(path, "/") || path == "/" || !validRootPolicy(policy) {
		return nil, fmt.Errorf("invalid trusted root: %w", os.ErrInvalid)
	}
	parts := strings.Split(path[1:], "/")
	for _, name := range parts {
		if !trustedComponent(name) {
			return nil, fmt.Errorf("invalid trusted component: %w", os.ErrInvalid)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r := &TrustedRoot{backend: b, policy: policy, path: path}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.Close())
		}
	}()
	fd, err := b.openRoot()
	if err != nil {
		return nil, err
	}
	r.chain = append(r.chain, trustedLink{fd: fd})
	r.chain[0].node, err = b.stat(fd)
	if err != nil {
		return nil, err
	}
	if err = r.checkLink(0, false); err != nil {
		return nil, err
	}
	for i := 0; i < len(parts); i++ {
		name := parts[i]
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		parent := fd
		fd, err = b.openDir(parent, name, false)
		if err != nil && i == 0 && len(r.aliases) == 0 && errors.Is(err, ErrUnsafeComponent) {
			n, statErr := b.statAt(parent, name)
			if statErr != nil {
				return nil, statErr
			}
			if n.mode&unix.S_IFMT != unix.S_IFLNK {
				return nil, err
			}
			if n.uid != 0 || n.links != 1 {
				return nil, denied("untrusted OS alias owner", nil)
			}
			target, aliasErr := b.osAlias(parent, name)
			if aliasErr != nil {
				return nil, aliasErr
			}
			r.aliases = append(r.aliases, trustedAlias{name: name, target: target, node: n})
			parts = append([]string{"private", name}, parts[1:]...)
			i--
			fd = parent
			continue
		}
		if errors.Is(err, unix.ENOENT) && i == len(parts)-1 && policy.AllowCreate {
			fd, err = r.createDir(ctx, parent, name, false, policy)
		}
		if err != nil {
			return nil, err
		}
		r.chain = append(r.chain, trustedLink{fd: fd, name: name, application: i == len(parts)-1})
		r.chain[len(r.chain)-1].node, err = b.stat(fd)
		if err != nil {
			return nil, err
		}
		if r.chain[len(r.chain)-1].node.mode&unix.S_IFMT != unix.S_IFDIR {
			return nil, ErrUnsafeComponent
		}
		if err = r.checkLink(len(r.chain)-1, i == len(parts)-1); err != nil {
			return nil, err
		}
	}
	if err = r.verify(); err != nil {
		return nil, err
	}
	return r, nil
}

func trustedComponent(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}
func validRootPolicy(p RootPolicy) bool { return p.Mode == 0700 || p.Mode == 0711 || p.Mode == 0755 }
func denied(operation string, err error) error {
	return fmt.Errorf("%s: %w", operation, errors.Join(os.ErrPermission, err))
}
func (r *TrustedRoot) checkLink(i int, application bool) error {
	l := r.chain[i]
	n := l.node
	if n.mode&unix.S_IFMT != unix.S_IFDIR {
		return ErrUnsafeComponent
	}
	if n.uid != 0 && n.uid != r.policy.Owner {
		return denied("untrusted directory owner", nil)
	}
	if n.mode&0022 != 0 {
		return denied("writable directory ancestor", nil)
	}
	if application && (n.uid != r.policy.Owner || n.mode&07777 != r.policy.Mode) {
		return denied("application directory permissions", nil)
	}
	if err := r.backend.checkFS(l.fd); err != nil {
		return denied("untrusted filesystem", err)
	}
	if err := r.backend.checkACL(l.fd, application, r.policy.Owner); err != nil {
		return denied("untrusted directory ACL", err)
	}
	return nil
}

func (r *TrustedRoot) verify() error {
	if len(r.chain) == 0 {
		return os.ErrClosed
	}
	for _, a := range r.aliases {
		n, err := r.backend.statAt(r.chain[0].fd, a.name)
		if err != nil {
			return err
		}
		target, err := r.backend.osAlias(r.chain[0].fd, a.name)
		if err != nil {
			return err
		}
		if n != a.node || target != a.target {
			return denied("OS alias changed", ErrIdentityMismatch)
		}
	}
	for i, l := range r.chain {
		n, err := r.backend.stat(l.fd)
		if err != nil {
			return err
		}
		if !sameTrustedDirectory(n, l.node) {
			return denied("directory identity or authority changed", ErrIdentityMismatch)
		}
		if i > 0 {
			n, err = r.backend.statAt(r.chain[i-1].fd, l.name)
			if err != nil {
				return err
			}
			if !sameTrustedDirectory(n, l.node) {
				return denied("directory name changed", ErrIdentityMismatch)
			}
		}
		if err := r.backend.checkACL(l.fd, l.application, r.policy.Owner); err != nil {
			return denied("directory ACL changed", err)
		}
		if err := r.backend.checkFS(l.fd); err != nil {
			return denied("directory filesystem changed", err)
		}
	}
	return nil
}

func sameTrustedDirectory(a, b trustedNode) bool {
	// Subdirectory creation changes nlink; zero means the held directory was
	// unlinked. It must never be confused with a usable namespace capability.
	if a.links == 0 || b.links == 0 {
		return false
	}
	a.links = 0
	b.links = 0
	return a == b
}

func (r *TrustedRoot) createDir(ctx context.Context, parent int, name string, below bool, p RootPolicy) (_ int, err error) {
	if err = ctx.Err(); err != nil {
		return -1, err
	}
	if err = r.verify(); err != nil {
		return -1, err
	}
	if err = r.backend.checkACL(parent, true, p.Owner); err != nil {
		return -1, denied("creation parent ACL", err)
	}
	if err = r.backend.mkdir(parent, name, 0700); err != nil {
		return -1, err
	} // EEXIST is never adopted.
	fd, err := r.backend.openDir(parent, name, below)
	if err != nil {
		return -1, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.backend.close(fd))
		}
	}()
	initial, err := r.backend.stat(fd)
	if err != nil {
		return -1, err
	}
	if initial.mode&unix.S_IFMT != unix.S_IFDIR {
		return -1, ErrUnsafeComponent
	}
	if initial.uid != p.Owner && initial.uid != uint32(os.Geteuid()) {
		return -1, denied("new directory owner changed", nil)
	}
	if err = r.backend.checkFS(fd); err != nil {
		return -1, denied("new directory filesystem", err)
	}
	named, err := r.backend.statAt(parent, name)
	if err != nil {
		return -1, err
	}
	if named != initial {
		return -1, denied("new directory replaced", ErrIdentityMismatch)
	}
	if err = r.backend.prepareDir(fd, p.Owner, p.Mode); err != nil {
		return -1, err
	}
	final, err := r.backend.stat(fd)
	if err != nil {
		return -1, err
	}
	if final.id != initial.id || final.uid != p.Owner || final.mode&07777 != p.Mode {
		return -1, denied("new directory verification", nil)
	}
	if err = r.backend.checkACL(fd, true, p.Owner); err != nil {
		return -1, denied("new directory ACL", err)
	}
	if err = r.backend.sync(fd); err != nil {
		return -1, err
	}
	if err = r.backend.sync(parent); err != nil {
		return -1, err
	}
	// mkdir changes only the retained parent's link count. Do not adopt any
	// simultaneous owner/mode/identity change while refreshing that count.
	i := len(r.chain) - 1
	node, err := r.backend.stat(parent)
	if err != nil {
		return -1, err
	}
	before := r.chain[i].node
	before.links = node.links
	if before != node {
		return -1, denied("creation parent changed", ErrIdentityMismatch)
	}
	r.chain[i].node = node
	return fd, nil
}

// Close releases the retained descriptors. Repeated calls are harmless.
func (r *TrustedRoot) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		done := r.closeDone
		r.mu.Unlock()
		<-done
		return r.closeErr
	}
	r.closed = true
	r.closeDone = make(chan struct{})
	done, operation := r.closeDone, r.operation
	if r.closing != nil {
		close(r.closing)
	}
	r.mu.Unlock()
	// Wait for the current operation owner without holding the state mutex.
	// New and queued operations observe closing and never take ownership.
	if operation != nil {
		<-operation
	}
	var errs []error
	for i := len(r.chain) - 1; i >= 0; i-- {
		errs = append(errs, r.backend.close(r.chain[i].fd))
	}
	r.mu.Lock()
	r.chain = nil
	r.closeErr = errors.Join(errs...)
	r.mu.Unlock()
	close(done)
	return r.closeErr
}

// begin transfers exclusive operation ownership. The mutex only protects
// lifecycle bookkeeping; all fd IO occurs after it is released. No worker
// goroutine is needed, and queued callers can cancel while another owns IO.
func (r *TrustedRoot) begin(ctx context.Context) (func(), error) {
	if r == nil {
		return nil, os.ErrClosed
	}
	r.mu.Lock()
	if r.closed || len(r.chain) == 0 {
		r.mu.Unlock()
		return nil, os.ErrClosed
	}
	if r.operation == nil {
		r.operation = make(chan struct{}, 1)
		r.operation <- struct{}{}
		r.closing = make(chan struct{})
	}
	operation, closing := r.operation, r.closing
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-closing:
		return nil, os.ErrClosed
	case <-operation:
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		operation <- struct{}{}
		return nil, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		operation <- struct{}{}
		return nil, err
	}
	return func() { operation <- struct{}{} }, nil
}

type nativeTrustedBackend struct{}

func (nativeTrustedBackend) dup(fd int) (int, error) { return dupCLOEXEC(fd) }
func (nativeTrustedBackend) mkdir(fd int, name string, mode uint32) error {
	return unix.Mkdirat(fd, name, mode)
}
func (nativeTrustedBackend) sync(fd int) error { return unix.Fsync(fd) }
func (b nativeTrustedBackend) prepareDir(fd int, owner, mode uint32) error {
	n, err := b.stat(fd)
	if err != nil {
		return err
	}
	if n.uid != uint32(os.Geteuid()) {
		return denied("new directory owner changed", nil)
	}
	if err := clearTrustedDirectoryACL(fd); err != nil {
		return err
	}
	if n.uid != owner {
		if err := unix.Fchown(fd, int(owner), -1); err != nil {
			return err
		}
	}
	return unix.Fchmod(fd, mode)
}

const trustedDirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

func (nativeTrustedBackend) openRoot() (int, error) { return unix.Open("/", trustedDirFlags, 0) }
func (nativeTrustedBackend) stat(fd int) (trustedNode, error) {
	var st unix.Stat_t
	err := unix.Fstat(fd, &st)
	return trustedNodeFromStat(st), err
}
func (nativeTrustedBackend) statAt(fd int, name string) (trustedNode, error) {
	var st unix.Stat_t
	err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	return trustedNodeFromStat(st), err
}
func trustedNodeFromStat(st unix.Stat_t) trustedNode {
	return trustedNode{id: identFromStat(&st), uid: st.Uid, gid: st.Gid, mode: uint32(st.Mode), links: uint64(st.Nlink)}
}
func (nativeTrustedBackend) close(fd int) error { return unix.Close(fd) }
func componentOpenError(err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return errors.Join(ErrUnsafeComponent, err)
	}
	return err
}
