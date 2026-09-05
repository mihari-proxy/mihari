//go:build linux || darwin

package platform

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnedLease_PersistentLockContendsAndReleases(t *testing.T) {
	r, path := trustedTempCapability(t)
	l, err := acquireRootLock(context.Background(), r, "daemon.lock")
	if err != nil {
		t.Fatalf("acquire valid lock: %v", err)
	}
	t.Cleanup(func() { l.close() })
	before, err := os.Stat(filepath.Join(path, "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := acquireRootLock(context.Background(), r, "daemon.lock")
	if other != nil {
		other.close()
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("missing contention: %v", err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(path, "daemon.lock"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("lock inode not retained: %v", err)
	}
	other, err = acquireRootLock(context.Background(), r, "daemon.lock")
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	other.close()
}

type leaseCasefoldBackend struct{ nativeTrustedBackend }

func (b leaseCasefoldBackend) openFile(fd int, name string, flags int, mode uint32) (int, error) {
	return b.nativeTrustedBackend.openFile(fd, strings.ToLower(name), flags, mode)
}

type leasePrivilegedCasefold struct{ leasePrivilegedModel }

func (b leasePrivilegedCasefold) openFile(fd int, name string, flags int, mode uint32) (int, error) {
	return b.nativeTrustedBackend.openFile(fd, strings.ToLower(name), flags, mode)
}
func (b leasePrivilegedCasefold) statAt(fd int, name string) (trustedNode, error) {
	return b.leasePrivilegedModel.statAt(fd, strings.ToLower(name))
}

func TestOwnedLease_FreeDefaultAliasRejectedByParentIdentity(t *testing.T) {
	p, e := leaseTempDir(t), leaseTempDir(t)
	if err := os.Chmod(e, 0711); err != nil {
		t.Fatal(err)
	}
	defaults := platformLayoutDefaults("")
	defaults.BaseDir = filepath.Join(t.TempDir(), "canonical-system-base")
	open := func(ctx context.Context, path string, policy RootPolicy, parent bool) (*TrustedRoot, error) {
		actual := path
		if path == defaults.BaseDir {
			actual = e
		}
		fd, err := unix.Open(actual, trustedDirFlags, 0)
		if err != nil {
			return nil, err
		}
		b := leasePrivilegedCasefold{}
		n, err := b.stat(fd)
		if err != nil {
			b.close(fd)
			return nil, err
		}
		return &TrustedRoot{backend: b, policy: policy, path: path, parentOnly: parent, chain: []trustedLink{{fd: fd, node: n, application: true}}}, nil
	}
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: p, Data: NewPaths(p), ControlEndpoint: filepath.Join(e, "Control.sock")}
	l, err := acquireDaemonLease(context.Background(), layout, 0, defaults, open)
	if l != nil {
		l.Close()
	}
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("free default alias accepted: %v", err)
	}
	if _, err := os.Stat(layout.ControlEndpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("endpoint was created: %v", err)
	}
	r, err := open(context.Background(), p, RootPolicy{Mode: 0700}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	lock, err := acquireRootLock(context.Background(), r, "daemon.lock")
	if err != nil {
		t.Fatalf("failed alias acquisition leaked data lock: %v", err)
	}
	lock.close()
}
func (b leaseCasefoldBackend) statAt(fd int, name string) (trustedNode, error) {
	return b.nativeTrustedBackend.statAt(fd, strings.ToLower(name))
}

func TestOwnedLease_EquivalentBasenamesContend(t *testing.T) {
	d1, d2, e := leaseTempDir(t), leaseTempDir(t), leaseTempDir(t)
	open := func(ctx context.Context, path string, p RootPolicy, parent bool) (*TrustedRoot, error) {
		r, err := leaseFixtureOpen(ctx, path, p, parent)
		if err == nil {
			r.backend = leaseCasefoldBackend{}
		}
		return r, err
	}
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: d1, Data: NewPaths(d1), ControlEndpoint: filepath.Join(e, "Control.sock")}
	first, err := acquireDaemonLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), open)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	layout.BaseDir = d2
	layout.Data = NewPaths(d2)
	layout.ControlEndpoint = filepath.Join(e, "control.sock")
	second, err := acquireDaemonLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), open)
	if second != nil {
		second.Close()
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("equivalent socket names split lock domain: %v", err)
	}
	layout.ControlEndpoint = filepath.Join(e, "other.sock")
	second, err = acquireDaemonLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), open)
	if err != nil {
		t.Fatalf("different endpoint in same parent blocked: %v", err)
	}
	second.Close()
}

func TestOwnedLease_FreeDefaultEndpointRejectedBeforeIO(t *testing.T) {
	d := platformLayoutDefaults("")
	p := leaseTempDir(t)
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: p, Data: NewPaths(p), ControlEndpoint: filepath.Join(d.BaseDir, "control.sock")}
	open := func(context.Context, string, RootPolicy, bool) (*TrustedRoot, error) {
		t.Fatal("default endpoint attempted acquisition")
		return nil, os.ErrInvalid
	}
	if _, err := acquireDaemonLease(context.Background(), layout, uint32(os.Geteuid()), d, open); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("default endpoint accepted: %v", err)
	}
}

// This models UID 0 metadata over ordinary held-fd fixtures. It tests ordering
// and lock lifetime only; privileged ownership acceptance remains a T19 gate.
type leasePrivilegedModel struct{ nativeTrustedBackend }

func (b leasePrivilegedModel) stat(fd int) (trustedNode, error) {
	n, e := b.nativeTrustedBackend.stat(fd)
	n.uid = 0
	return n, e
}
func (b leasePrivilegedModel) statAt(fd int, name string) (trustedNode, error) {
	n, e := b.nativeTrustedBackend.statAt(fd, name)
	n.uid = 0
	return n, e
}

func TestOwnedLease_RootServiceInstallOrder(t *testing.T) {
	base, p := leaseTempDir(t), leaseTempDir(t)
	if err := os.Chmod(base, 0711); err != nil {
		t.Fatal(err)
	}
	defaults := platformLayoutDefaults("")
	defaults.BaseDir = base
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: p, Data: NewPaths(p), ControlEndpoint: filepath.Join(p, "control.sock")}
	var calls []string
	open := func(ctx context.Context, path string, policy RootPolicy, parent bool) (*TrustedRoot, error) {
		calls = append(calls, path)
		if parent || policy.Owner != 0 {
			t.Fatal("incorrect service root policy")
		}
		if path == p {
			if _, err := os.Stat(filepath.Join(base, "install.lock")); err != nil {
				t.Fatal("P opened before B lock")
			}
		}
		fd, err := unix.Open(path, trustedDirFlags, 0)
		if err != nil {
			return nil, err
		}
		b := leasePrivilegedModel{}
		n, err := b.stat(fd)
		if err != nil {
			b.close(fd)
			return nil, err
		}
		return &TrustedRoot{backend: b, policy: policy, path: path, chain: []trustedLink{{fd: fd, node: n, application: true}}}, nil
	}
	l, err := acquireInstallLease(context.Background(), layout, 0, defaults, open)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if len(calls) != 2 || calls[0] != base || calls[1] != p {
		t.Fatalf("service acquisition order %v", calls)
	}
	if err := l.Validate(context.Background(), layout, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base, p} {
		r, err := open(context.Background(), path, RootPolicy{Mode: 0700}, false)
		if err != nil {
			t.Fatal(err)
		}
		other, err := acquireRootLock(context.Background(), r, "install.lock")
		if other != nil {
			other.close()
		}
		r.Close()
		if !errors.Is(err, ErrLeaseConflict) {
			t.Fatalf("missing install domain: %v", err)
		}
	}
}

func TestOwnedLease_SystemNonrootFailsBeforeAcquisition(t *testing.T) {
	d := platformLayoutDefaults("")
	layout := ResolvedLayout{Mode: SystemMode, BaseDir: d.BaseDir, Data: NewPaths(filepath.Join(d.BaseDir, "data")), ControlEndpoint: filepath.Join(d.BaseDir, "control.sock")}
	open := func(context.Context, string, RootPolicy, bool) (*TrustedRoot, error) {
		t.Fatal("nonroot attempted service filesystem acquisition")
		return nil, os.ErrPermission
	}
	if _, err := acquireInstallLease(context.Background(), layout, 1000, d, open); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if _, err := acquireDaemonLease(context.Background(), layout, 1000, d, open); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
}

func TestOwnedLease_BorrowHoldsOwnerAndChecksAfterCallback(t *testing.T) {
	p := leaseTempDir(t)
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: p, Data: NewPaths(p), ControlEndpoint: filepath.Join(p, "control.sock")}
	l, err := acquireDaemonLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), leaseFixtureOpen)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- l.Borrow().WithEndpoint(context.Background(), layout, func(string) error { close(entered); <-release; return nil })
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- l.Close() }()
	select {
	case <-l.state.closing:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Close did not start")
	}
	select {
	case <-closed:
		close(release)
		t.Fatal("borrow released owner")
	default:
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	l, err = acquireDaemonLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), leaseFixtureOpen)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	err = l.WithEndpoint(context.Background(), layout, func(string) error { return os.Rename(filepath.Join(p, "daemon.lock"), filepath.Join(p, "moved.lock")) })
	if err == nil {
		t.Fatal("post-callback lock replacement accepted")
	}
}

func TestOwnedLease_ExistingParentModePolicy(t *testing.T) {
	b := newTrustedModel()
	n := b.nodes[3]
	n.mode = unix.S_IFDIR | 0750
	b.nodes[3] = n
	r, err := openTrustedRootKind(context.Background(), "/var/lib/external", RootPolicy{Mode: 0755}, b, true)
	if err != nil {
		t.Fatalf("safe existing parent: %v", err)
	}
	r.Close()
	if b.prepared != 0 || b.created != 0 || b.nodes[3].mode != unix.S_IFDIR|0750 {
		t.Fatal("existing parent repaired")
	}
}

type leaseMountBoundaryBackend struct {
	*modelTrustedBackend
	crossed bool
}

func (b *leaseMountBoundaryBackend) openDir(fd int, name string, below bool) (int, error) {
	if below && fd == 2 {
		b.crossed = true
		return -1, os.ErrPermission
	}
	return b.modelTrustedBackend.openDir(fd, name, below)
}

func TestOwnedLease_EndpointBelowDataRejectsNestedMount(t *testing.T) {
	b := &leaseMountBoundaryBackend{modelTrustedBackend: newTrustedModel()}
	parent, err := openTrustedRoot(context.Background(), "/var/lib/endpoint", RootPolicy{Mode: 0700}, b)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	anchor := &TrustedRoot{chain: append([]trustedLink(nil), parent.chain[:3]...)}
	if err := verifyEndpointMountBoundary(anchor, parent); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("endpoint below anchor crossed mount: %v", err)
	}
	if !b.crossed {
		t.Fatal("did not check anchored mount boundary")
	}
}

func TestOwnedLease_PrivateMaintenanceScope(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unprivileged maintenance scope")
	}
	p := leaseTempDir(t)
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: p, Data: NewPaths(p), ControlEndpoint: filepath.Join(p, "control.sock")}
	l, err := acquireInstallLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), leaseFixtureOpen)
	if err != nil {
		t.Fatalf("private maintenance lease: %v", err)
	}
	defer l.Close()
	if err := l.Validate(context.Background(), layout, false); err != nil {
		t.Fatal(err)
	}
	if err := l.Validate(context.Background(), layout, true); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("maintenance masqueraded as service lease: %v", err)
	}
	second, err := acquireInstallLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), leaseFixtureOpen)
	if second != nil {
		second.Close()
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("install contention missing: %v", err)
	}
}

func TestOwnedLease_SubprocessExitReleases(t *testing.T) {
	if path := os.Getenv("MIHARI_TEST_LEASE_CHILD"); path != "" {
		r, err := leaseFixtureOpen(context.Background(), path, RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700}, false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = acquireRootLock(context.Background(), r, "daemon.lock")
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("held")
		bufio.NewReader(os.Stdin).ReadString('\n')
		// Deliberately exit without closing the lease: the kernel must release it.
		os.Exit(0)
	}
	p := leaseTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnedLease_SubprocessExitReleases$")
	cmd.Env = append(os.Environ(), "MIHARI_TEST_LEASE_CHILD="+p)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		cancel()
		in.Close()
		if !waited {
			cmd.Wait()
		}
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "held\n" {
		t.Fatalf("child acquire: %q %v", line, err)
	}
	r, err := leaseFixtureOpen(ctx, p, RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, err := acquireRootLock(ctx, r, "daemon.lock")
	if l != nil {
		l.close()
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("child did not hold lock: %v", err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatal(err)
	}
	l, err = acquireRootLock(ctx, r, "daemon.lock")
	if err != nil {
		t.Fatalf("process exit did not release: %v", err)
	}
	l.close()
}

// Only the acquisition boundary is replaced: all held-fd, ACL, mode, identity,
// mount and flock operations below these temporary anchors remain native.
func leaseFixtureOpen(ctx context.Context, path string, p RootPolicy, parent bool) (*TrustedRoot, error) {
	fd, err := unix.Open(path, trustedDirFlags, 0)
	if err != nil {
		return nil, err
	}
	b := nativeTrustedBackend{}
	n, err := b.stat(fd)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	r := &TrustedRoot{backend: b, policy: p, path: path, parentOnly: parent, chain: []trustedLink{{fd: fd, node: n, application: true}}}
	if err := r.checkLink(0, true); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func TestOwnedLease_DaemonDomains(t *testing.T) {
	ctx := context.Background()
	uid := uint32(os.Geteuid())
	defaults := platformLayoutDefaults("")
	d1, d2, e1, e2 := leaseTempDir(t), leaseTempDir(t), leaseTempDir(t), leaseTempDir(t)
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: d1, Data: NewPaths(d1), ControlEndpoint: filepath.Join(e1, "control.sock")}
	first, err := acquireDaemonLease(ctx, layout, uid, defaults, leaseFixtureOpen)
	if err != nil {
		t.Fatalf("valid daemon lease: %v", err)
	}
	defer first.Close()
	for _, domain := range []string{"same data different endpoint", "different data same endpoint"} {
		t.Run(domain, func(t *testing.T) {
			other := layout
			if domain == "same data different endpoint" {
				other.ControlEndpoint = filepath.Join(e2, "other.sock")
			} else {
				other.BaseDir = d2
				other.Data = NewPaths(d2)
			}
			second, err := acquireDaemonLease(ctx, other, uid, defaults, leaseFixtureOpen)
			if second != nil {
				second.Close()
			}
			if !errors.Is(err, ErrLeaseConflict) {
				t.Fatalf("domain did not contend: %v", err)
			}
		})
	}
	other := layout
	other.BaseDir = d2
	other.Data = NewPaths(d2)
	other.ControlEndpoint = filepath.Join(e2, "other.sock")
	second, err := acquireDaemonLease(ctx, other, uid, defaults, leaseFixtureOpen)
	if err != nil {
		t.Fatalf("independent domains blocked or partial lock leaked: %v", err)
	}
	second.Close()
	view := first.Borrow()
	if err := view.Validate(ctx, layout); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := view.Validate(ctx, layout); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("borrow survived owner close: %v", err)
	}
}

func TestOwnedLease_ExternalParent0750Preserved(t *testing.T) {
	d, e := leaseTempDir(t), leaseTempDir(t)
	if err := os.Chmod(e, 0750); err != nil {
		t.Fatal(err)
	}
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: d, Data: NewPaths(d), ControlEndpoint: filepath.Join(e, "control.sock")}
	l, err := acquireDaemonLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), leaseFixtureOpen)
	if err != nil {
		t.Fatalf("safe external parent rejected: %v", err)
	}
	l.Close()
	n, err := os.Stat(e)
	if err != nil || n.Mode().Perm() != 0750 {
		t.Fatalf("parent modified: %v %v", n, err)
	}
}

func TestOwnedLease_RejectsReplacedName(t *testing.T) {
	r, path := trustedTempCapability(t)
	l, err := acquireRootLock(context.Background(), r, "daemon.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	if err := os.Rename(filepath.Join(path, "daemon.lock"), filepath.Join(path, "old.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.validate(); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("replaced lock name accepted: %v", err)
	}
}

func leaseTempDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOwnedLease_BinaryParentOnly(t *testing.T) {
	p := leaseTempDir(t)
	calls := 0
	open := func(ctx context.Context, path string, policy RootPolicy, parent bool) (*TrustedRoot, error) {
		calls++
		if path != p || !parent || policy.AllowCreate {
			t.Fatalf("binary lease crossed scope: %q %#v", path, policy)
		}
		return leaseFixtureOpen(ctx, path, policy, parent)
	}
	l, err := acquireBinaryLease(context.Background(), p, uint32(os.Geteuid()), open)
	if err != nil {
		t.Fatalf("binary lease: %v", err)
	}
	defer l.Close()
	if calls != 1 {
		t.Fatalf("acquisitions %d", calls)
	}
	if err := l.Validate(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	second, err := acquireBinaryLease(context.Background(), p, uint32(os.Geteuid()), open)
	if second != nil {
		second.Close()
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("binary domain missing: %v", err)
	}
}
