//go:build linux || darwin

package platform

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrLeaseConflict reports another holder of a data, install or endpoint lock.
var ErrLeaseConflict = errors.New("lease already held")

type rootLock struct {
	root *TrustedRoot
	fd   int
	name string
	node trustedNode
}

type leaseState struct {
	capabilityLifetime
	layout                     ResolvedLayout
	owner                      uint32
	global                     bool
	roots                      []*TrustedRoot
	locks                      []*rootLock
	dataAnchor, endpointParent *TrustedRoot
}

// OwnedInstallLease owns service-install locks. Do not copy after use.
type OwnedInstallLease struct{ state *leaseState }

// OwnedDaemonLease owns data and endpoint locks. Do not copy after use.
type OwnedDaemonLease struct{ state *leaseState }

// OwnedBinaryLease owns only the verified binary-parent update lock.
type OwnedBinaryLease struct {
	state      *leaseState
	parentPath string
}

// BorrowedDaemonLease provides endpoint operations without release authority.
type BorrowedDaemonLease struct{ state *leaseState }

// AcquireBinaryLease locks an existing binary parent without acquiring service,
// private-data or daemon locks. The caller must separately verify its binary.
func AcquireBinaryLease(ctx context.Context, parentPath string) (*OwnedBinaryLease, error) {
	return acquireBinaryLease(ctx, parentPath, uint32(os.Geteuid()), nativeLeaseRoot)
}
func acquireBinaryLease(ctx context.Context, parentPath string, owner uint32, open leaseRootOpener) (_ *OwnedBinaryLease, err error) {
	if !filepath.IsAbs(parentPath) || filepath.Clean(parentPath) != parentPath {
		return nil, os.ErrInvalid
	}
	s := &leaseState{owner: owner}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	r, err := open(ctx, parentPath, RootPolicy{Owner: owner, Mode: 0755}, true)
	if err != nil {
		return nil, err
	}
	s.roots = append(s.roots, r)
	if err = s.addLock(ctx, r, ".mihari-binary.lock"); err != nil {
		return nil, err
	}
	if err = s.validate(s.layout); err != nil {
		return nil, err
	}
	return &OwnedBinaryLease{state: s, parentPath: parentPath}, nil
}

// Close releases the binary update lock and its retained parent.
func (l *OwnedBinaryLease) Close() error {
	if l == nil || l.state == nil {
		return nil
	}
	return l.state.close()
}

// Validate checks the requested binary-parent scope and every retained identity.
func (l *OwnedBinaryLease) Validate(ctx context.Context, parentPath string) error {
	if l == nil || l.state == nil {
		return os.ErrClosed
	}
	if parentPath != l.parentPath {
		return os.ErrInvalid
	}
	finish, err := l.state.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return l.state.validate(l.state.layout)
}

type leaseRootOpener func(context.Context, string, RootPolicy, bool) (*TrustedRoot, error)

func nativeLeaseRoot(ctx context.Context, path string, p RootPolicy, parent bool) (*TrustedRoot, error) {
	if parent {
		return openTrustedParent(ctx, path, p.Owner)
	}
	return OpenTrustedRoot(ctx, path, p)
}

// AcquireInstallLease obtains B then P for root private services, B for system
// services, or P alone for unprivileged private maintenance.
func AcquireInstallLease(ctx context.Context, layout ResolvedLayout) (*OwnedInstallLease, error) {
	return acquireInstallLease(ctx, layout, uint32(os.Geteuid()), platformLayoutDefaults(""), nativeLeaseRoot)
}
func acquireInstallLease(ctx context.Context, layout ResolvedLayout, owner uint32, defaults LayoutDefaults, open leaseRootOpener) (_ *OwnedInstallLease, err error) {
	if err = validateLeaseLayout(layout, owner, defaults); err != nil {
		return nil, err
	}
	s := &leaseState{layout: layout, owner: owner, global: owner == 0}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	if owner == 0 {
		base, e := open(ctx, defaults.BaseDir, RootPolicy{Owner: 0, Mode: 0711, AllowCreate: true}, false)
		if e != nil {
			return nil, e
		}
		s.roots = append(s.roots, base)
		if err = s.addLock(ctx, base, "install.lock"); err != nil {
			return nil, err
		}
	}
	if layout.Mode == PrivateMode {
		private, e := open(ctx, layout.BaseDir, RootPolicy{Owner: owner, Mode: 0700, AllowCreate: true}, false)
		if e != nil {
			return nil, e
		}
		s.roots = append(s.roots, private)
		if len(s.roots) > 1 && rootsOverlap(s.roots[0], private) {
			return nil, os.ErrInvalid
		}
		if err = s.addLock(ctx, private, "install.lock"); err != nil {
			return nil, err
		}
	}
	if err = s.validate(layout); err != nil {
		return nil, err
	}
	return &OwnedInstallLease{state: s}, nil
}

func rootsOverlap(a, b *TrustedRoot) bool {
	ai, bi := a.chain[len(a.chain)-1].node.id, b.chain[len(b.chain)-1].node.id
	for _, l := range a.chain {
		if l.node.id == bi {
			return true
		}
	}
	for _, l := range b.chain {
		if l.node.id == ai {
			return true
		}
	}
	return false
}

// AcquireDaemonLease obtains the data lock before the endpoint lock.
func AcquireDaemonLease(ctx context.Context, layout ResolvedLayout) (*OwnedDaemonLease, error) {
	return acquireDaemonLease(ctx, layout, uint32(os.Geteuid()), platformLayoutDefaults(""), nativeLeaseRoot)
}
func acquireDaemonLease(ctx context.Context, layout ResolvedLayout, owner uint32, defaults LayoutDefaults, open leaseRootOpener) (_ *OwnedDaemonLease, err error) {
	if err = validateLeaseLayout(layout, owner, defaults); err != nil {
		return nil, err
	}
	s := &leaseState{layout: layout, owner: owner}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	baseMode := uint32(0700)
	if layout.Mode == SystemMode {
		baseMode = 0711
	}
	base, err := open(ctx, layout.BaseDir, RootPolicy{Owner: owner, Mode: baseMode, AllowCreate: true}, false)
	if err != nil {
		return nil, err
	}
	s.roots = append(s.roots, base)
	data := base
	if layout.Mode == SystemMode {
		data, err = base.OpenDir(ctx, "data", RootPolicy{Owner: owner, Mode: 0700, AllowCreate: true})
		if err != nil {
			return nil, err
		}
		s.roots = append(s.roots, data)
	}
	if err = s.addLock(ctx, data, "daemon.lock"); err != nil {
		return nil, err
	}
	parentPath := filepath.Dir(layout.ControlEndpoint)
	parent := base
	if parentPath == layout.Data.Root {
		parent = data
	} else if parentPath != layout.BaseDir {
		parent, err = open(ctx, parentPath, RootPolicy{Owner: owner, Mode: 0755}, true)
		if err != nil {
			return nil, err
		}
		s.roots = append(s.roots, parent)
	}
	// A literal-name guard lets the directory's own case/Unicode lookup rules
	// unify equivalent socket spellings before the required basename hash lock.
	guardName := endpointGuardName(layout.ControlEndpoint)
	s.dataAnchor, s.endpointParent = base, parent
	if err = verifyEndpointMountBoundary(base, parent); err != nil {
		return nil, err
	}
	if len(guardName) > 255 {
		return nil, os.ErrInvalid
	}
	if err = s.addLock(ctx, parent, guardName); err != nil {
		return nil, err
	}
	if layout.Mode == PrivateMode && owner == 0 {
		if err = s.rejectDefaultAlias(ctx, base, parent, defaults, open); err != nil {
			return nil, err
		}
	}
	if err = s.addLock(ctx, parent, endpointLockName(layout.ControlEndpoint)); err != nil {
		return nil, err
	}
	if err = s.validate(layout); err != nil {
		return nil, err
	}
	return &OwnedDaemonLease{state: s}, nil
}

func validateLeaseLayout(layout ResolvedLayout, owner uint32, defaults LayoutDefaults) error {
	if layout.Mode != SystemMode && layout.Mode != PrivateMode {
		return os.ErrInvalid
	}
	if layout.Mode == SystemMode && owner != 0 {
		return os.ErrPermission
	}
	for _, p := range []string{layout.BaseDir, layout.Data.Root, layout.ControlEndpoint} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || !trustedComponent(filepath.Base(p)) {
			return os.ErrInvalid
		}
	}
	if len(layout.ControlEndpoint) > defaults.SocketLimit {
		return os.ErrInvalid
	}
	if layout.Mode == SystemMode {
		if layout.BaseDir != defaults.BaseDir || layout.Data.Root != filepath.Join(defaults.BaseDir, "data") {
			return os.ErrInvalid
		}
	} else {
		if layout.BaseDir != layout.Data.Root || pathsOverlap(defaults.OS, layout.Data.Root, defaults.BaseDir) || layout.ControlEndpoint == filepath.Join(defaults.BaseDir, "control.sock") {
			return os.ErrInvalid
		}
	}
	return nil
}

func (s *leaseState) rejectDefaultAlias(ctx context.Context, data, parent *TrustedRoot, defaults LayoutDefaults, open leaseRootOpener) error {
	// Nonroot private parents cannot be root-owned B. Root private instances
	// must also exclude aliases of default B by held identity, even with no E.
	system, err := open(ctx, defaults.BaseDir, RootPolicy{Owner: 0, Mode: 0755}, true)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	s.roots = append(s.roots, system)
	if rootsOverlap(data, system) {
		return os.ErrInvalid
	}
	if parent.chain[len(parent.chain)-1].node.id != system.chain[len(system.chain)-1].node.id {
		return nil
	}
	guard := s.locks[len(s.locks)-1]
	n, err := parent.backend.statAt(parent.chain[len(parent.chain)-1].fd, endpointGuardName("control.sock"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if n.id == guard.node.id {
		return os.ErrInvalid
	}
	return nil
}
func (s *leaseState) addLock(ctx context.Context, r *TrustedRoot, name string) error {
	l, err := acquireRootLock(ctx, r, name)
	if err != nil {
		return err
	}
	s.locks = append(s.locks, l)
	return nil
}

func endpointLockName(endpoint string) string {
	return fmt.Sprintf(".mihari-endpoint-%x.lock", sha256.Sum256([]byte(filepath.Base(endpoint))))
}

func endpointGuardName(endpoint string) string {
	return ".mihari-endpoint-name." + filepath.Base(endpoint)
}

// Close releases endpoint/data/install locks in reverse acquisition order.
func (l *OwnedInstallLease) Close() error {
	if l == nil || l.state == nil {
		return nil
	}
	return l.state.close()
}

// Close releases endpoint before data, after any borrowed operation finishes.
func (l *OwnedDaemonLease) Close() error {
	if l == nil || l.state == nil {
		return nil
	}
	return l.state.close()
}
func (s *leaseState) close() error {
	return s.closeWith(func() error {
		var errs []error
		for i := len(s.locks) - 1; i >= 0; i-- {
			errs = append(errs, s.locks[i].close())
		}
		for i := len(s.roots) - 1; i >= 0; i-- {
			errs = append(errs, s.roots[i].Close())
		}
		return errors.Join(errs...)
	})
}

// Borrow returns a view that cannot close or unlock the owner.
func (l *OwnedDaemonLease) Borrow() BorrowedDaemonLease {
	if l == nil {
		return BorrowedDaemonLease{}
	}
	return BorrowedDaemonLease{state: l.state}
}

// WithEndpoint validates the held namespace before and after a socket operation.
// The callback receives a pathname only; it must not retain the borrowed scope.
func (l *OwnedDaemonLease) WithEndpoint(ctx context.Context, layout ResolvedLayout, fn func(string) error) error {
	return l.Borrow().WithEndpoint(ctx, layout, fn)
}

// WithEndpoint borrows the owner's operation scope without reacquiring locks.
func (l BorrowedDaemonLease) WithEndpoint(ctx context.Context, layout ResolvedLayout, fn func(string) error) error {
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
	if fn == nil {
		return os.ErrInvalid
	}
	err = fn(s.layout.ControlEndpoint)
	return errors.Join(err, s.validate(layout), ctx.Err())
}

// Validate proves that the view still names the requested data and endpoint.
func (l BorrowedDaemonLease) Validate(ctx context.Context, layout ResolvedLayout) error {
	return l.WithEndpoint(ctx, layout, func(string) error { return nil })
}

// Validate verifies install scope and retained identities. requireGlobal rejects
// an unprivileged maintenance lease when a system-service action is requested.
func (l *OwnedInstallLease) Validate(ctx context.Context, layout ResolvedLayout, requireGlobal bool) error {
	if l == nil || l.state == nil {
		return os.ErrClosed
	}
	s := l.state
	finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if requireGlobal && (!s.global || s.owner != 0) {
		return os.ErrPermission
	}
	return s.validate(layout)
}
func (s *leaseState) validate(layout ResolvedLayout) error {
	if layout != s.layout {
		return os.ErrInvalid
	}
	for _, r := range s.roots {
		if err := r.verify(); err != nil {
			return err
		}
	}
	if s.dataAnchor != nil {
		if err := verifyEndpointMountBoundary(s.dataAnchor, s.endpointParent); err != nil {
			return err
		}
	}
	for _, l := range s.locks {
		if err := l.validate(); err != nil {
			return err
		}
	}
	return nil
}

func verifyEndpointMountBoundary(anchor, parent *TrustedRoot) error {
	// Absolute external-parent acquisition allows OS mount boundaries before its
	// anchor. If it is actually below our selected B/P, reapply NO_XDEV before
	// creating locks and on every borrowed use, using retained descriptors only.
	id := anchor.chain[len(anchor.chain)-1].node.id
	below := false
	for i, l := range parent.chain {
		if below {
			fd, err := parent.backend.openDir(parent.chain[i-1].fd, l.name, true)
			if err != nil {
				return err
			}
			n, statErr := parent.backend.stat(fd)
			closeErr := parent.backend.close(fd)
			if statErr != nil || closeErr != nil {
				return errors.Join(statErr, closeErr)
			}
			if !sameTrustedDirectory(n, l.node) {
				return ErrIdentityMismatch
			}
		}
		if l.node.id == id {
			below = true
		}
	}
	return nil
}

func acquireRootLock(ctx context.Context, root *TrustedRoot, name string) (_ *rootLock, err error) {
	finish, err := root.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if !trustedComponent(name) {
		return nil, os.ErrInvalid
	}
	if err = root.verify(); err != nil {
		return nil, err
	}
	parent := root.chain[len(root.chain)-1].fd
	if err = root.backend.checkACL(parent, true, root.policy.Owner); err != nil {
		return nil, denied("lock creation parent ACL", err)
	}
	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	fd, err := root.backend.openFile(parent, name, flags|unix.O_CREAT|unix.O_EXCL, 0600)
	created := err == nil
	if errors.Is(err, unix.EEXIST) {
		fd, err = root.backend.openFile(parent, name, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, root.backend.close(fd))
		}
	}()
	n, err := root.checkFile(fd, 0600)
	if err != nil {
		return nil, err
	}
	if n.mode&07777 != 0600 {
		return nil, denied("lock permissions", nil)
	}
	if err = root.checkFileName(parent, name, n); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrLeaseConflict
		}
		return nil, err
	}
	if err = root.verify(); err != nil {
		return nil, err
	}
	if err = root.checkFileName(parent, name, n); err != nil {
		return nil, err
	}
	if created {
		if err = root.backend.sync(fd); err != nil {
			return nil, err
		}
		if err = root.backend.sync(parent); err != nil {
			return nil, err
		}
	}
	return &rootLock{root: root, fd: fd, name: name, node: n}, nil
}
func (l *rootLock) validate() error {
	if l.fd < 0 {
		return os.ErrClosed
	}
	if err := l.root.verify(); err != nil {
		return err
	}
	n, err := l.root.checkFile(l.fd, 0600)
	if err != nil {
		return err
	}
	if n != l.node {
		return denied("lock identity changed", ErrIdentityMismatch)
	}
	return l.root.checkFileName(l.root.chain[len(l.root.chain)-1].fd, l.name, n)
}
func (l *rootLock) close() error {
	if l.fd < 0 {
		return nil
	}
	fd := l.fd
	l.fd = -1
	return l.root.backend.close(fd)
}
