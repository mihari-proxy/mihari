//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func credentialLeaseFixture(t *testing.T, external bool) (*OwnedDaemonLease, ResolvedLayout) {
	t.Helper()
	root := leaseTempDir(t)
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: root, Data: NewPaths(root), ControlEndpoint: filepath.Join(root, "control.sock"), CredentialPath: filepath.Join(root, "control.token")}
	if external {
		parent := leaseTempDir(t)
		if err := os.Chmod(parent, 0750); err != nil {
			t.Fatal(err)
		}
		layout.CredentialPath = filepath.Join(parent, "control.token")
	}
	l, err := acquireDaemonLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), leaseFixtureOpen)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { assertTestClose(t, l.Close) })
	return l, layout
}

func TestControlCredential_RejectsUnsafeExistingAndInvalidScopes(t *testing.T) {
	for _, kind := range []string{"short", "long", "symlink", "hardlink", "fifo", "wide"} {
		t.Run(kind, func(t *testing.T) {
			l, layout := credentialLeaseFixture(t, false)
			raw := []byte(strings.Repeat("a", 64))
			switch kind {
			case "short":
				raw = []byte("damaged")
			case "long":
				raw = []byte(strings.Repeat("a", 66))
			case "fifo":
				if err := unix.Mkfifo(layout.CredentialPath, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink":
				target := filepath.Join(layout.BaseDir, "original")
				if err := os.WriteFile(target, raw, 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "symlink" {
					err = os.Symlink(target, layout.CredentialPath)
				} else {
					err = os.Link(target, layout.CredentialPath)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if kind == "short" || kind == "long" || kind == "wide" {
				mode := os.FileMode(0600)
				if kind == "wide" {
					mode = 0644
				}
				if err := os.WriteFile(layout.CredentialPath, raw, mode); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(layout.CredentialPath)
			if err != nil {
				t.Fatal(err)
			}
			err = l.Borrow().WithCredential(context.Background(), layout, func(c *ControlCredentialFile) error {
				if _, err := c.Read(context.Background()); err == nil {
					t.Fatal("unsafe credential read accepted")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.Lstat(layout.CredentialPath)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("unsafe object modified")
			}
		})
	}
	l, layout := credentialLeaseFixture(t, false)
	call := func(*ControlCredentialFile) error { t.Fatal("invalid scope entered callback"); return nil }
	if err := (BorrowedDaemonLease{}).WithCredential(context.Background(), layout, call); !errors.Is(err, os.ErrClosed) {
		t.Fatal("empty lease accepted")
	}
	other := layout
	other.CredentialPath += "other"
	if err := l.Borrow().WithCredential(context.Background(), other, call); !errors.Is(err, os.ErrInvalid) {
		t.Fatal("mismatched layout accepted")
	}
	if err := l.Borrow().WithCredential(context.Background(), layout, nil); !errors.Is(err, os.ErrInvalid) {
		t.Fatal("nil callback accepted")
	}
	assertTestClose(t, l.Close)
	if err := l.Borrow().WithCredential(context.Background(), layout, call); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed lease accepted")
	}
}

type credentialBlockingSync struct {
	nativeTrustedBackend
	entered, release        chan struct{}
	once                    sync.Once
	publishing, earlyVerify atomic.Bool
}

func (b *credentialBlockingSync) sync(fd int) error {
	b.once.Do(func() { b.publishing.Store(true); close(b.entered); <-b.release; b.publishing.Store(false) })
	return b.nativeTrustedBackend.sync(fd)
}
func (b *credentialBlockingSync) checkACL(fd int, strict bool, owner uint32) error {
	if b.publishing.Load() {
		b.earlyVerify.Store(true)
	}
	return b.nativeTrustedBackend.checkACL(fd, strict, owner)
}

func TestControlCredential_ScopeJoinsConcurrentUseBeforeClosingParent(t *testing.T) {
	l, layout := credentialLeaseFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b := &credentialBlockingSync{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(b.release) }) }
	defer release()
	l.state.dataAnchor.backend = b
	scoped := make(chan *ControlCredentialFile, 1)
	done := make(chan error, 1)
	writeDone := make(chan error, 1)
	go func() {
		done <- l.Borrow().WithCredential(ctx, layout, func(c *ControlCredentialFile) error {
			scoped <- c
			go func() { writeDone <- c.Create(ctx, []byte(strings.Repeat("a", 64))) }()
			select {
			case <-b.entered:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	var c *ControlCredentialFile
	select {
	case c = <-scoped:
	case err := <-done:
		t.Fatalf("scope did not begin: %v", err)
	case <-ctx.Done():
		t.Fatal("scope did not begin")
	}
	select {
	case <-b.entered:
	case <-ctx.Done():
		t.Fatal("publication did not begin")
	}
	select {
	case <-c.closing:
	case <-ctx.Done():
		t.Fatal("scope did not invalidate outstanding capability")
	}
	select {
	case err := <-done:
		t.Fatalf("scope returned before in-flight publication: %v", err)
	default:
	}
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- l.Close() }()
	select {
	case <-l.state.closing:
	case <-ctx.Done():
		t.Fatal("owner did not begin closing")
	}
	select {
	case err := <-ownerDone:
		t.Fatalf("owner closed during credential publication: %v", err)
	default:
	}
	release()
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-ownerDone; err != nil {
		t.Fatal(err)
	}
	if b.earlyVerify.Load() {
		t.Fatal("scope verified before concurrent publication finished")
	}
	if _, err := c.Read(ctx); !errors.Is(err, os.ErrClosed) {
		t.Fatal("scope reopened")
	}
}

func TestControlCredential_SystemPublicationModeModel(t *testing.T) {
	// UID metadata is modeled; the actual descriptors, locks and publication
	// are real ordinary-UID fixtures. This is not root/dual-UID acceptance.
	root := leaseTempDir(t)
	if err := os.Chmod(root, 0711); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	layout := ResolvedLayout{Mode: SystemMode, BaseDir: root, Data: NewPaths(filepath.Join(root, "data")), ControlEndpoint: filepath.Join(root, "control.sock"), CredentialPath: filepath.Join(root, "control.token")}
	defaults := platformLayoutDefaults("")
	defaults.BaseDir = root
	open := func(ctx context.Context, path string, p RootPolicy, parent bool) (*TrustedRoot, error) {
		owner := p.Owner
		p.Owner = uint32(os.Geteuid())
		r, err := leaseFixtureOpen(ctx, path, p, parent)
		if err != nil {
			return nil, err
		}
		r.policy.Owner = owner
		r.backend = leasePrivilegedModel{}
		for i := range r.chain {
			r.chain[i].node.uid = 0
		}
		return r, nil
	}
	l, err := acquireDaemonLease(context.Background(), layout, 0, defaults, open)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, l.Close)
	err = l.Borrow().WithCredential(context.Background(), layout, func(c *ControlCredentialFile) error {
		return c.Create(context.Background(), []byte(strings.Repeat("a", 64)))
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(layout.CredentialPath)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatal("system credential did not publish0644")
	}
	info, err = os.Stat(root)
	if err != nil || info.Mode().Perm() != 0711 {
		t.Fatal("base lost search-only public access")
	}
}

func TestControlCredential_RepeatedScopesDoNotRetainRootsOrDescriptors(t *testing.T) {
	l, layout := credentialLeaseFixture(t, true)
	// Model the root-private case over ordinary fds so an auxiliary default
	// lookup would succeed. No privileged pathname is opened by the fixture.
	l.state.owner = 0
	for _, r := range l.state.roots {
		r.backend = leasePrivilegedModel{}
		r.policy.Owner = 0
		for i := range r.chain {
			r.chain[i].node.uid = 0
		}
	}
	for _, lock := range l.state.locks {
		lock.node.uid = 0
	}
	aux := leaseTempDir(t)
	wantRoots := len(l.state.roots)
	for range 3 {
		var openedFDs []int
		open := func(ctx context.Context, path string, p RootPolicy, parent bool) (*TrustedRoot, error) {
			actual := path
			if path == platformLayoutDefaults("").BaseDir {
				actual = aux
			}
			p.Owner = uint32(os.Geteuid())
			r, err := leaseFixtureOpen(ctx, actual, p, parent)
			if err != nil {
				return nil, err
			}
			r.backend = leasePrivilegedModel{}
			r.policy.Owner = 0
			r.path = path
			for i := range r.chain {
				r.chain[i].node.uid = 0
				openedFDs = append(openedFDs, r.chain[i].fd)
			}
			return r, nil
		}
		var retained *ControlCredentialFile
		err := l.Borrow().withCredential(context.Background(), layout, func(c *ControlCredentialFile) error {
			retained = c
			_, err := c.Read(context.Background())
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}, open)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.state.roots) != wantRoots {
			t.Fatal("credential scope leaked auxiliary roots into daemon lease")
		}
		for _, fd := range openedFDs {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Fatal("external parent descriptor remains open after scope")
			}
		}
		if _, err := retained.Read(context.Background()); !errors.Is(err, os.ErrClosed) {
			t.Fatal("retained scope remained usable")
		}
	}
}

func TestControlCredential_MissingExternalParentIsNotCreated(t *testing.T) {
	root := leaseTempDir(t)
	missing := filepath.Join(root, "unknown-parent")
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: root, Data: NewPaths(root), ControlEndpoint: filepath.Join(root, "control.sock"), CredentialPath: filepath.Join(missing, "control.token")}
	l, err := acquireDaemonLease(context.Background(), layout, uint32(os.Geteuid()), platformLayoutDefaults(""), leaseFixtureOpen)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, l.Close)
	err = l.Borrow().withCredential(context.Background(), layout, func(*ControlCredentialFile) error { t.Error("missing parent entered credential callback"); return nil }, leaseFixtureOpen)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing parent not refused: %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("daemon created unknown external parent")
	}
}

func TestControlCredential_PublishReadAndScope(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "base", true: "external0750"}[external], func(t *testing.T) {
			l, layout := credentialLeaseFixture(t, external)
			ctx := context.Background()
			raw := []byte(strings.Repeat("a", 64) + "\n")
			var retained *ControlCredentialFile
			err := l.Borrow().withCredential(ctx, layout, func(c *ControlCredentialFile) error {
				retained = c
				if _, err := c.Read(ctx); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing credential: %v", err)
				}
				if err := c.Create(ctx, raw); err != nil {
					return err
				}
				got, err := c.Read(ctx)
				if err != nil || string(got) != string(raw) {
					t.Fatalf("published credential read: %v", err)
				}
				if err := c.Create(ctx, []byte(strings.Repeat("b", 64))); !errors.Is(err, os.ErrExist) {
					t.Fatalf("existing credential overwritten: %v", err)
				}
				return nil
			}, leaseFixtureOpen)
			if err != nil {
				t.Fatalf("valid credential scope: %v", err)
			}
			if _, err := retained.Read(ctx); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("retained read capability: %v", err)
			}
			if err := retained.Create(ctx, raw); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("retained writer capability: %v", err)
			}
			info, err := os.Stat(layout.CredentialPath)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("credential permissions: %v", err)
			}
			if external {
				info, err := os.Stat(filepath.Dir(layout.CredentialPath))
				if err != nil || info.Mode().Perm() != 0750 {
					t.Fatal("external parent permissions changed")
				}
			}
		})
	}
}
