//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPrivateFSCapability_TransferClosesWholeChainOnce(t *testing.T) {
	b := newTrustedModel()
	r, err := openTrustedRoot(context.Background(), "/var/lib/private", RootPolicy{Mode: 0700}, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { assertTestClose(t, r.Close) })
	fs, err := NewPrivateFSFromRoot(r)
	if err != nil {
		t.Fatalf("transfer valid root: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("old owner Close blocked")
	}
	if len(b.closed) != 0 {
		t.Fatal("old owner closed moved descriptors")
	}
	if _, err := r.begin(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("old owner usable: %v", err)
	}
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.closed) != 4 {
		t.Fatalf("closed %d descriptors, want 4", len(b.closed))
	}
	for i, fd := range b.closed {
		if fd != 3-i {
			t.Fatalf("descriptor close sequence %v", b.closed)
		}
	}
}

func TestPrivateFSCapability_ChildRejectionDoesNotRepair(t *testing.T) {
	r, path := privateTempCapability(t)
	r.path = path
	fs, err := NewPrivateFSFromRoot(r)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, fs.Close)
	child := filepath.Join(path, "logs")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	if err := fs.EnsureDir(child); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unsafe child accepted: %v", err)
	}
	n, err := os.Stat(child)
	if err != nil || n.Mode().Perm() != 0755 {
		t.Fatalf("child repaired: %v %v", n, err)
	}
}

func TestPrivateFSCapability_AppendRejectsWideFile(t *testing.T) {
	r, path := privateTempCapability(t)
	r.path = path
	fs, err := NewPrivateFSFromRoot(r)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, fs.Close)
	if err := fs.EnsureDir("logs"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(path, "logs", "unsafe.log")
	if err := os.WriteFile(p, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := fs.OpenAppend(p)
	if f != nil {
		f.Close()
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unsafe append accepted: %v", err)
	}
	n, err := os.Stat(p)
	if err != nil || n.Mode().Perm() != 0644 {
		t.Fatalf("file repaired: %v %v", n, err)
	}
}

func TestPrivateFSCapability_RejectsUnsafeReadsAndMutations(t *testing.T) {
	for _, op := range []string{"read", "rename", "remove"} {
		t.Run(op, func(t *testing.T) {
			r, path := privateTempCapability(t)
			r.path = path
			fs, err := NewPrivateFSFromRoot(r)
			if err != nil {
				t.Fatal(err)
			}
			defer assertTestClose(t, fs.Close)
			if err := fs.EnsureDir("logs"); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(path, "logs", "public.log")
			if err := os.WriteFile(p, []byte("preserve"), 0644); err != nil {
				t.Fatal(err)
			}
			entries, err := fs.ReadDir("logs")
			if err != nil {
				t.Fatal(err)
			}
			switch op {
			case "read":
				f, e := fs.OpenReadChecked(p, entries[0].Identity)
				err = e
				if f != nil {
					f.Close()
				}
			case "rename":
				err = fs.Rename(p, filepath.Join(path, "logs", "moved.log"))
			case "remove":
				err = fs.Remove(p)
			}
			if !errors.Is(err, os.ErrPermission) {
				t.Fatalf("unsafe %s accepted: %v", op, err)
			}
			b, err := os.ReadFile(p)
			if err != nil || string(b) != "preserve" {
				t.Fatalf("source modified: %v", err)
			}
		})
	}
}

func TestPrivateFSCapability_PublishRetainsAncestry(t *testing.T) {
	r, path := privateTempCapability(t)
	r.path = path
	fs, err := NewPrivateFSFromRoot(r)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, fs.Close)
	if err := fs.EnsureDir("logs-export"); err != nil {
		t.Fatal(err)
	}
	d, err := fs.OpenPublishDir("logs-export")
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, d.Close)
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, w.Close)
	f, name, err := w.CreateTemp("export-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("archive")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := d.PublishNoReplace(w, name, "archive.zip", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(path, "logs-export")
	if err := os.Rename(old, old+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(old, 0700); err != nil {
		t.Fatal(err)
	}
	w, err = d.CreateWorkspace()
	if w != nil {
		assertTestClose(t, w.Close)
	}
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("publish accepted replaced ancestor: %v", err)
	}
}

func TestPrivateFSCapability_RejectPublicRootPreservesOwnership(t *testing.T) {
	b := newTrustedModel()
	n := b.nodes[3]
	n.mode = unix.S_IFDIR | 0711
	b.nodes[3] = n
	r, err := openTrustedRoot(context.Background(), "/var/lib/mihari", RootPolicy{Mode: 0711}, b)
	if err != nil {
		t.Fatal(err)
	}
	if fs, err := NewPrivateFSFromRoot(r); fs != nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("public root accepted: %v", err)
	}
	if b.nodes[3].mode != unix.S_IFDIR|0711 || len(b.closed) != 0 {
		t.Fatal("failed transfer changed root")
	}
	if err := r.verify(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.closed) != 4 {
		t.Fatal("caller lost ownership")
	}
}

func TestPrivateFSCapability_FailedVerificationPreservesOwner(t *testing.T) {
	b := newTrustedModel()
	r, err := openTrustedRoot(context.Background(), "/var/lib/private", RootPolicy{Mode: 0700}, b)
	if err != nil {
		t.Fatal(err)
	}
	b.replaced = 2
	fs, err := NewPrivateFSFromRoot(r)
	if fs != nil || !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("stale transfer accepted: %v", err)
	}
	if len(b.closed) != 0 {
		t.Fatal("failed transfer freed caller descriptors")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.closed) != 4 {
		t.Fatal("failed transfer lost descriptors")
	}
}

type capabilityFaultBackend struct {
	trustedBackend
	failACL, failFS bool
}

type capabilityRenameBackend struct {
	trustedBackend
	sourceChecks int
	replace      func()
}

func (b *capabilityRenameBackend) statAt(fd int, name string) (trustedNode, error) {
	if name == "source.log" {
		b.sourceChecks++
		if b.sourceChecks == 2 {
			b.replace()
		}
	}
	return b.trustedBackend.statAt(fd, name)
}

func TestPrivateFSCapability_ReplaceRechecksDestination(t *testing.T) {
	r, path := privateTempCapability(t)
	r.path = path
	b := &capabilityRenameBackend{trustedBackend: r.backend}
	r.backend = b
	fs, err := NewPrivateFSFromRoot(r)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, fs.Close)
	if err := fs.EnsureDir("logs"); err != nil {
		t.Fatal(err)
	}
	source, target := filepath.Join(path, "logs", "source.log"), filepath.Join(path, "logs", "target.log")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	b.replace = func() {
		if err := os.Rename(target, target+"-old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("foreign"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	err = fs.trustedRename("logs", "source.log", "target.log", true)
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("replaced destination accepted: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "foreign" {
		t.Fatalf("replacement clobbered: %q %v", data, err)
	}
}

func (b *capabilityFaultBackend) checkACL(fd int, strict bool, owner uint32) error {
	if b.failACL {
		return os.ErrPermission
	}
	return b.trustedBackend.checkACL(fd, strict, owner)
}
func (b *capabilityFaultBackend) checkFS(fd int) error {
	if b.failFS {
		return os.ErrPermission
	}
	return b.trustedBackend.checkFS(fd)
}

func TestPrivateFSCapability_PublishRejectsAuthorityChangesAndPreservesReplacements(t *testing.T) {
	for _, change := range []string{"ACL", "filesystem", "replaced temp"} {
		t.Run(change, func(t *testing.T) {
			r, path := privateTempCapability(t)
			r.path = path
			b := &capabilityFaultBackend{trustedBackend: r.backend}
			r.backend = b
			fs, err := NewPrivateFSFromRoot(r)
			if err != nil {
				t.Fatal(err)
			}
			defer assertTestClose(t, fs.Close)
			if err := fs.EnsureDir("logs-export"); err != nil {
				t.Fatal(err)
			}
			d, err := fs.OpenPublishDir("logs-export")
			if err != nil {
				t.Fatal(err)
			}
			defer assertTestClose(t, d.Close)
			w, err := d.CreateWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			defer assertTestClose(t, w.Close)
			f, name, err := w.CreateTemp("archive-*")
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
			p := filepath.Join(path, "logs-export", w.name, name)
			switch change {
			case "ACL":
				b.failACL = true
			case "filesystem":
				b.failFS = true
			case "replaced temp":
				if err := os.Rename(p, p+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := d.PublishNoReplace(w, name, "archive.zip", nil); err == nil {
				t.Fatal("published after authority change")
			}
			if err := w.Close(); err == nil {
				t.Fatal("unsafe cleanup succeeded")
			}
			if change == "replaced temp" {
				data, err := os.ReadFile(p)
				if err != nil || string(data) != "foreign" {
					t.Fatalf("cleanup removed replacement: %v", err)
				}
			} else {
				f, _, err := w.CreateTemp("denied-*")
				if f != nil {
					f.Close()
				}
				if !errors.Is(err, os.ErrClosed) {
					t.Fatal(err)
				}
			}
		})
	}
}

// Like T02's retained-fd fixture, this does not authorize /tmp ancestry. It
// additionally establishes the exact private application mode for the bridge.
func privateTempCapability(t *testing.T) (*TrustedRoot, string) {
	t.Helper()
	r, path := trustedTempCapability(t)
	fd := r.chain[0].fd
	if err := unix.Fchmod(fd, 0700); err != nil {
		t.Fatal(err)
	}
	n, err := r.backend.stat(fd)
	if err != nil {
		t.Fatal(err)
	}
	r.chain[0].node = n
	r.path = path
	return r, path
}

// Only the targeted child changes process-global umask. Its anchor is created
// before the change; ordinary tests and concurrent child processes are isolated.
func runPrivateUmaskSubprocess(t *testing.T, check func(*TrustedRoot, string)) {
	t.Helper()
	if os.Getenv("MIHARI_TEST_PRIVATE_UMASK") == t.Name() {
		r, path := privateTempCapability(t)
		previous := unix.Umask(0200)
		defer unix.Umask(previous)
		check(r, path)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), "MIHARI_TEST_PRIVATE_UMASK="+t.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("umask child: %v\n%s", err, out)
	}
}

func assertCreatedMode(t *testing.T, path string, want os.FileMode) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Errorf("created %s mode=%04o, want%04o", filepath.Base(path), info.Mode().Perm(), want)
	}
	return info
}

func TestPrivateFSCapability_RestrictiveUmask(t *testing.T) {
	runPrivateUmaskSubprocess(t, func(r *TrustedRoot, path string) {
		fs, err := NewPrivateFSFromRoot(r)
		if err != nil {
			t.Fatal(err)
		}
		defer assertTestClose(t, fs.Close)
		for _, dir := range []string{"logs", "logs-export"} {
			if err := fs.EnsureDir(dir); err != nil {
				t.Fatal(err)
			}
		}
		t.Run("append", func(t *testing.T) {
			p := filepath.Join(path, "logs", "new.log")
			f, err := fs.OpenAppend(p)
			if err != nil {
				t.Fatalf("create append under umask0200: %v", err)
			}
			if _, err := f.WriteString("entry"); err != nil {
				f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			assertCreatedMode(t, p, 0600)
		})
		t.Run("temp", func(t *testing.T) {
			f, err := fs.CreateTemp("logs", "private-*")
			if err != nil {
				t.Fatal(err)
			}
			name := f.Name()
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			assertCreatedMode(t, filepath.Join(path, "logs", name), 0600)
		})
		t.Run("published", func(t *testing.T) {
			d, err := fs.OpenPublishDir("logs-export")
			if err != nil {
				t.Fatal(err)
			}
			defer assertTestClose(t, d.Close)
			w, err := d.CreateWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			defer assertTestClose(t, w.Close)
			f, name, err := w.CreateTemp("archive-*")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString("archive"); err != nil {
				f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			assertCreatedMode(t, filepath.Join(path, "logs-export", w.name, name), 0600)
			if err := d.PublishNoReplace(w, name, "archive.zip", nil); err != nil {
				t.Fatal(err)
			}
			assertCreatedMode(t, filepath.Join(path, "logs-export", "archive.zip"), 0600)
		})
		t.Run("existing files", func(t *testing.T) {
			for _, mode := range []os.FileMode{0400, 0644} {
				p := filepath.Join(path, "logs", fmt.Sprintf("existing-%o.log", mode))
				if err := os.WriteFile(p, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(p, mode); err != nil {
					t.Fatal(err)
				}
				f, err := fs.OpenAppend(p)
				if f != nil {
					f.Close()
				}
				if !errors.Is(err, os.ErrPermission) {
					t.Fatalf("unsafe existing append accepted: %v", err)
				}
				assertCreatedMode(t, p, mode)
				data, err := os.ReadFile(p)
				if err != nil || string(data) != "preserve" {
					t.Fatalf("existing file modified: %q %v", data, err)
				}
			}
		})
	})
}

type privateInitializationBackend struct {
	nativeTrustedBackend
	reason  string
	created bool
	name    string
}

func (b *privateInitializationBackend) openFile(parent int, name string, flags int, mode uint32) (int, error) {
	fd, err := b.nativeTrustedBackend.openFile(parent, name, flags, mode)
	if err == nil && flags&unix.O_EXCL != 0 {
		b.created = true
		b.name = name
	}
	return fd, err
}

func (b *privateInitializationBackend) stat(fd int) (trustedNode, error) {
	n, err := b.nativeTrustedBackend.stat(fd)
	if b.created && n.mode&unix.S_IFMT == unix.S_IFREG {
		switch b.reason {
		case "type":
			n.mode = n.mode&^unix.S_IFMT | unix.S_IFIFO
		case "owner":
			n.uid++
		case "links":
			n.links = 2
		}
	}
	return n, err
}

func (b *privateInitializationBackend) statAt(parent int, name string) (trustedNode, error) {
	n, err := b.nativeTrustedBackend.statAt(parent, name)
	if b.created && b.reason == "name" && name == b.name {
		n.id.ino++
	}
	return n, err
}

func (b *privateInitializationBackend) checkACL(fd int, strict bool, owner uint32) error {
	n, err := b.nativeTrustedBackend.stat(fd)
	if err != nil {
		return err
	}
	if b.created && ((b.reason == "ACL" && n.mode&unix.S_IFMT == unix.S_IFREG) || (b.reason == "parent ACL" && n.mode&unix.S_IFMT == unix.S_IFDIR)) {
		return os.ErrPermission
	}
	return b.nativeTrustedBackend.checkACL(fd, strict, owner)
}

func TestPrivateFSCapability_InitializationValidationBeforeModeChange(t *testing.T) {
	runPrivateUmaskSubprocess(t, func(r *TrustedRoot, path string) {
		b := &privateInitializationBackend{}
		r.backend = b
		fs, err := NewPrivateFSFromRoot(r)
		if err != nil {
			t.Fatal(err)
		}
		defer assertTestClose(t, fs.Close)
		if err := fs.EnsureDir("logs"); err != nil {
			t.Fatal(err)
		}
		for _, route := range []string{"lock", "append", "temp"} {
			for _, reason := range []string{"type", "owner", "links", "ACL", "name", "parent ACL"} {
				t.Run(route+"/"+reason, func(t *testing.T) {
					b.created = false
					b.reason = reason
					name := "new-" + route + "-" + reason
					var err error
					parent := path
					switch route {
					case "lock":
						var lock *rootLock
						lock, err = acquireRootLock(context.Background(), fs.plat.trusted, name)
						if lock != nil {
							assertTestClose(t, lock.close)
						}
					case "append":
						parent = filepath.Join(path, "logs")
						var f *os.File
						f, err = fs.OpenAppend(filepath.Join(parent, name))
						if f != nil {
							f.Close()
						}
					case "temp":
						parent = filepath.Join(path, "logs")
						var f *os.File
						f, err = fs.CreateTemp("logs", "untrusted-*")
						if f != nil {
							f.Close()
						}
					}
					if err == nil {
						t.Fatal("untrusted created inode accepted")
					}
					if !b.created {
						t.Fatal("failure occurred before exclusive file creation")
					}
					assertCreatedMode(t, filepath.Join(parent, b.name), 0400)
				})
			}
		}
	})
}
