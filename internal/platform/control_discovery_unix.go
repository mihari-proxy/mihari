//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrControlData identifies invalid credential size or an incomplete read.
var ErrControlData = errors.New("invalid control credential data")

type discoveryRef struct {
	fd    int
	tail  string
	owned bool
}
type discoveryMount struct {
	id    uint64
	fsid  [2]int32
	kind  string
	flags uint64
	owner uint32
}
type discoveryMetadata struct {
	node  trustedNode
	mount discoveryMount
	size  int64
}
type discoveryBackend interface {
	root() (discoveryRef, error)
	child(discoveryRef, string) (discoveryRef, error)
	directory(discoveryRef, string) (discoveryRef, error)
	inspect(discoveryRef, bool, uint32) (discoveryMetadata, error)
	name(discoveryRef, string) (trustedNode, error)
	alias(discoveryRef, string) (string, error)
	close(discoveryRef) error
	read(discoveryRef, string, discoveryMetadata) ([]byte, error)
}
type discoveryLink struct {
	ref      discoveryRef
	name     string
	metadata discoveryMetadata
	strict   bool
}
type discoveryAlias struct {
	root         discoveryRef
	name, target string
	node         trustedNode
}
type controlDiscovery struct {
	backend discoveryBackend
	locator ControlLocator
	chains  [][]discoveryLink
	aliases []discoveryAlias
	closed  bool
}

func openControlDiscovery(ctx context.Context, locator ControlLocator, path string, euid uint32, defaults LayoutDefaults, b discoveryBackend) (_ *controlDiscovery, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = validDiscoveryLocator(locator, euid, defaults); err != nil {
		return nil, err
	}
	if path != locator.Endpoint && path != locator.Credential {
		return nil, os.ErrInvalid
	}
	d := &controlDiscovery{backend: b, locator: locator}
	defer func() {
		if err != nil {
			err = errors.Join(err, d.Close())
		}
	}()
	base, err := d.walk(ctx, locator.BaseDir, nil, discoveryMode(locator), false)
	if err != nil {
		return nil, err
	}
	anchor := base[len(base)-1].metadata
	var system []discoveryLink
	if locator.Mode == PrivateMode && euid == 0 {
		// Root private aliases must not reintroduce the default machine B.
		other, otherErr := d.walk(ctx, defaults.BaseDir, nil, 0711, true)
		if otherErr != nil && !errors.Is(otherErr, os.ErrNotExist) {
			return nil, otherErr
		}
		if otherErr == nil {
			for _, a := range base {
				for _, c := range other {
					if a.metadata.node.id == other[len(other)-1].metadata.node.id || c.metadata.node.id == anchor.node.id {
						return nil, os.ErrInvalid
					}
				}
			}
		}
		if otherErr == nil {
			system = other
		}
	}
	target, err := d.walk(ctx, path, &anchor, 0, false)
	if err != nil {
		return nil, err
	}
	parent := target[len(target)-2].metadata.node
	if parent.uid != locator.ExpectedOwner {
		return nil, os.ErrPermission
	}
	if path == locator.Endpoint && len(system) > 0 && parent.id == system[len(system)-1].metadata.node.id {
		n, e := b.name(system[len(system)-1].ref, "control.sock")
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
		if e == nil && n.id == target[len(target)-1].metadata.node.id {
			return nil, os.ErrInvalid
		}
	}
	if err = d.verify(ctx); err != nil {
		return nil, err
	}
	return d, nil
}
func (d *controlDiscovery) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	var errs []error
	for i := len(d.chains) - 1; i >= 0; i-- {
		for j := len(d.chains[i]) - 1; j >= 0; j-- {
			r := d.chains[i][j].ref
			if r.owned {
				errs = append(errs, d.backend.close(r))
			}
		}
	}
	return errors.Join(errs...)
}
func (d *controlDiscovery) walk(ctx context.Context, path string, anchor *discoveryMetadata, baseMode uint32, auxiliaryParent bool) (_ []discoveryLink, err error) {
	r, err := d.backend.root()
	if err != nil {
		return nil, err
	}
	index := len(d.chains)
	d.chains = append(d.chains, []discoveryLink{{ref: r}})
	aliasStart := len(d.aliases)
	defer func() {
		if err != nil {
			for _, l := range d.chains[index] {
				if l.ref.owned {
					err = errors.Join(err, d.backend.close(l.ref))
				}
			}
			d.chains = d.chains[:index]
			d.aliases = d.aliases[:aliasStart]
		}
	}()
	m, err := d.backend.inspect(r, false, d.locator.ExpectedOwner)
	if err != nil {
		return nil, err
	}
	if err = checkDiscoveryDirectory(m, d.locator.ExpectedOwner, false, 0); err != nil {
		return nil, err
	}
	d.chains[index][0].metadata = m
	parts := strings.Split(path[1:], "/")
	below := false
	for i := 0; i < len(parts); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := parts[i]
		if !trustedComponent(name) {
			return nil, os.ErrInvalid
		}
		p := d.chains[index][len(d.chains[index])-1].ref
		n, err := d.backend.name(p, name)
		if err != nil {
			return nil, err
		}
		if n.mode&unix.S_IFMT == unix.S_IFLNK {
			if i != 0 || n.uid != 0 || n.links != 1 {
				return nil, ErrUnsafeComponent
			}
			target, e := d.backend.alias(p, name)
			if e != nil {
				return nil, e
			}
			d.aliases = append(d.aliases, discoveryAlias{root: p, name: name, target: target, node: n})
			parts = append([]string{"private", name}, parts[1:]...)
			i--
			continue
		}
		var ref discoveryRef
		if baseMode != 0 || i < len(parts)-1 {
			ref, err = d.backend.directory(p, name)
		} else {
			ref, err = d.backend.child(p, name)
		}
		if err != nil {
			return nil, err
		}
		d.chains[index] = append(d.chains[index], discoveryLink{ref: ref, name: name})
		j := len(d.chains[index]) - 1
		strict := below || (baseMode != 0 && i == len(parts)-1)
		m, err := d.backend.inspect(ref, strict, d.locator.ExpectedOwner)
		if err != nil {
			return nil, err
		}
		if m.node != n {
			return nil, ErrIdentityMismatch
		}
		if anchor != nil && m.node.id == anchor.node.id {
			strict = true
			m, err = d.backend.inspect(ref, true, d.locator.ExpectedOwner)
			if err != nil {
				return nil, err
			}
			if m != *anchor {
				return nil, ErrIdentityMismatch
			}
			below = true
		}
		if below && anchor != nil && (m.mount != anchor.mount || m.node.id.dev != anchor.node.id.dev) {
			return nil, os.ErrPermission
		}
		if baseMode != 0 || i < len(parts)-1 {
			mode := uint32(0700)
			if baseMode != 0 && i == len(parts)-1 {
				mode = baseMode
				// The nonselected default B is only an overlap witness for a
				// root private instance, matching T03 existing-parent policy.
				if auxiliaryParent {
					mode = m.node.mode & 07777
					if mode&07000 != 0 {
						return nil, os.ErrPermission
					}
				}
			} else if anchor != nil && m.node.id == anchor.node.id {
				mode = discoveryMode(d.locator)
			}
			if err = checkDiscoveryDirectory(m, d.locator.ExpectedOwner, strict, mode); err != nil {
				return nil, err
			}
		} else {
			parent := d.chains[index][j-1].metadata
			if m.mount != parent.mount || m.node.id.dev != parent.node.id.dev {
				return nil, os.ErrPermission
			}
			kind, maxMode := uint32(unix.S_IFSOCK), uint32(0600)
			if path == d.locator.Credential {
				kind = unix.S_IFREG
				if d.locator.Mode == SystemMode {
					maxMode = 0644
				}
			} else if d.locator.Mode == SystemMode {
				maxMode = 0666
			}
			if m.node.mode&unix.S_IFMT != kind || m.node.uid != d.locator.ExpectedOwner || m.node.links != 1 || m.node.mode&07777&^maxMode != 0 {
				return nil, os.ErrPermission
			}
			strict = true
			checked, e := d.backend.inspect(ref, true, d.locator.ExpectedOwner)
			if e != nil {
				return nil, e
			}
			if checked != m {
				return nil, ErrIdentityMismatch
			}
		}
		d.chains[index][j].metadata = m
		d.chains[index][j].strict = strict
	}
	return d.chains[index], nil
}
func (d *controlDiscovery) verify(ctx context.Context) error {
	if d.closed {
		return os.ErrClosed
	}
	for _, chain := range d.chains {
		for i, l := range chain {
			if err := ctx.Err(); err != nil {
				return err
			}
			m, err := d.backend.inspect(l.ref, l.strict, d.locator.ExpectedOwner)
			if err != nil {
				return err
			}
			if m != l.metadata {
				return ErrIdentityMismatch
			}
			if i > 0 {
				n, err := d.backend.name(chain[i-1].ref, l.name)
				if err != nil {
					return err
				}
				if n != l.metadata.node {
					return ErrIdentityMismatch
				}
			}
		}
	}
	for _, a := range d.aliases {
		n, err := d.backend.name(a.root, a.name)
		if err != nil {
			return err
		}
		target, err := d.backend.alias(a.root, a.name)
		if err != nil {
			return err
		}
		if n != a.node || target != a.target {
			return ErrIdentityMismatch
		}
	}
	return nil
}
func validDiscoveryLocator(l ControlLocator, euid uint32, defaults LayoutDefaults) error {
	for _, p := range []string{l.BaseDir, l.Endpoint, l.Credential} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || !trustedComponent(filepath.Base(p)) {
			return os.ErrInvalid
		}
		for _, name := range strings.Split(p[1:], "/") {
			if !trustedComponent(name) {
				return os.ErrInvalid
			}
		}
	}
	if len(l.Endpoint) > defaults.SocketLimit {
		return os.ErrInvalid
	}
	switch l.Mode {
	case SystemMode:
		if l.ExpectedOwner != 0 || l.BaseDir != defaults.BaseDir {
			return os.ErrInvalid
		}
	case PrivateMode:
		if l.ExpectedOwner != euid {
			return os.ErrPermission
		}
		if pathsOverlap(defaults.OS, l.BaseDir, defaults.BaseDir) || l.Endpoint == filepath.Join(defaults.BaseDir, "control.sock") {
			return os.ErrInvalid
		}
	default:
		return os.ErrInvalid
	}
	return nil
}

func discoveryMode(l ControlLocator) uint32 {
	if l.Mode == SystemMode {
		return 0711
	}
	return 0700
}
func checkDiscoveryDirectory(m discoveryMetadata, owner uint32, application bool, mode uint32) error {
	n := m.node
	if n.mode&unix.S_IFMT != unix.S_IFDIR {
		return ErrUnsafeComponent
	}
	if (n.uid != 0 && n.uid != owner) || n.mode&0022 != 0 {
		return os.ErrPermission
	}
	if application && (n.uid != owner || n.mode&07777 != mode) {
		return os.ErrPermission
	}
	return nil
}

// WithControlEndpoint retains a read-only namespace proof throughout fn.
func WithControlEndpoint(ctx context.Context, locator ControlLocator, fn func(string) error) error {
	if fn == nil {
		return os.ErrInvalid
	}
	d, err := openControlDiscovery(ctx, locator, locator.Endpoint, uint32(os.Geteuid()), platformLayoutDefaults(""), nativeDiscoveryBackend{})
	if err != nil {
		return err
	}
	err = fn(locator.Endpoint)
	return errors.Join(err, d.verify(ctx), d.Close())
}

// ReadControlCredential returns bounded, verified bytes without creating C.
// The credential package owns hexadecimal and optional newline validation.
func ReadControlCredential(ctx context.Context, locator ControlLocator) ([]byte, error) {
	d, err := openControlDiscovery(ctx, locator, locator.Credential, uint32(os.Geteuid()), platformLayoutDefaults(""), nativeDiscoveryBackend{})
	if err != nil {
		return nil, err
	}
	chain := d.chains[len(d.chains)-1]
	last := chain[len(chain)-1]
	data, err := d.backend.read(chain[len(chain)-2].ref, last.name, last.metadata)
	err = errors.Join(err, d.verify(ctx), d.Close())
	if err != nil {
		return nil, err
	}
	return data, nil
}

func readDiscoveryFD(fd int, expected discoveryMetadata, inspect func(int) (discoveryMetadata, error)) (_ []byte, err error) {
	f := os.NewFile(uintptr(fd), "control credential")
	defer func() { err = errors.Join(err, f.Close()) }()
	m, err := inspect(fd)
	if err != nil {
		return nil, err
	}
	if m != expected {
		return nil, ErrIdentityMismatch
	}
	if m.node.mode&unix.S_IFMT != unix.S_IFREG || m.node.links != 1 {
		return nil, os.ErrPermission
	}
	if m.size != 64 && m.size != 65 {
		return nil, ErrControlData
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return nil, err
	}
	if _, err = unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags&^unix.O_NONBLOCK); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil {
		return nil, err
	}
	if len(b) != int(m.size) {
		return nil, ErrControlData
	}
	after, err := inspect(fd)
	if err != nil {
		return nil, err
	}
	if after != m {
		return nil, ErrIdentityMismatch
	}
	return b, nil
}
