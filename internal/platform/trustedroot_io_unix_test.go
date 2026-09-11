//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This fixture exercises already-held fd sub-capabilities only. It does not
// claim t.TempDir or /tmp satisfies production absolute-root acquisition.
func trustedTempCapability(t *testing.T) (*TrustedRoot, string) {
	t.Helper()
	path := t.TempDir()
	fd, err := unix.Open(path, trustedDirFlags, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := nativeTrustedBackend{}
	n, err := b.stat(fd)
	if err != nil {
		if closeErr := unix.Close(fd); closeErr != nil {
			t.Errorf("close failed trusted-root fixture: %v", closeErr)
		}
		t.Fatal(err)
	}
	r := &TrustedRoot{backend: b, policy: RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700}, chain: []trustedLink{{fd: fd, node: n, application: true}}}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r, path
}

func TestTrustedRoot_AtomicFilePublication(t *testing.T) {
	r, path := trustedTempCapability(t)
	ctx := context.Background()
	if err := r.WriteFile(ctx, "control.token", []byte("first"), 0644, nil); err != nil {
		t.Fatalf("positive publish: %v", err)
	}
	f, id, err := r.OpenFile(ctx, "control.token", 0644)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(b) != "first" {
		t.Fatalf("published bytes %q: %v", b, err)
	}
	if err := r.WriteFile(ctx, "control.token", []byte("second"), 0644, &id); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile(ctx, "control.token", []byte("stale"), 0644, &id); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("stale replacement: %v", err)
	}
	if err := r.WriteFile(ctx, "control.token", []byte("clobber"), 0644, nil); !errors.Is(err, os.ErrExist) {
		t.Fatalf("no-replace: %v", err)
	}
	b, err = os.ReadFile(filepath.Join(path, "control.token"))
	if err != nil || string(b) != "second" {
		t.Fatalf("final bytes %q: %v", b, err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary leftovers: %v %v", entries, err)
	}
}

func TestTrustedRoot_FileRejectionsPreserveObject(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "wide mode"} {
		t.Run(kind, func(t *testing.T) {
			r, path := trustedTempCapability(t)
			ctx := context.Background()
			if err := r.WriteFile(ctx, "source", []byte("unchanged"), 0600, nil); err != nil {
				t.Fatalf("positive file: %v", err)
			}
			source, target := filepath.Join(path, "source"), filepath.Join(path, "target")
			switch kind {
			case "symlink":
				if err := os.Symlink("source", target); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(source, target); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(target, 0600); err != nil {
					t.Fatal(err)
				}
			case "wide mode":
				if err := os.WriteFile(target, []byte("public"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			f, _, err := r.OpenFile(ctx, "target", 0600)
			if f != nil {
				f.Close()
				t.Fatal("opened unsafe file")
			}
			if kind == "wide mode" {
				if !errors.Is(err, os.ErrPermission) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrUnsafeComponent) {
				t.Fatalf("wrong component rejection: %v", err)
			}
			b, err := os.ReadFile(source)
			if err != nil || string(b) != "unchanged" {
				t.Fatal("source mutated")
			}
		})
	}
}

func TestTrustedRoot_ChildCreationRetainsIndependentCapability(t *testing.T) {
	r, path := trustedTempCapability(t)
	child, err := r.OpenDir(context.Background(), "child", RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatalf("positive child creation: %v", err)
	}
	defer assertTestClose(t, child.Close)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.verify(); err != nil {
		t.Fatalf("parent close invalidated child: %v", err)
	}
	info, err := os.Stat(filepath.Join(path, "child"))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("created mode: %v %v", info, err)
	}
}

func TestTrustedRoot_ChildRejectsReplacedParent(t *testing.T) {
	r, path := trustedTempCapability(t)
	child, err := r.OpenDir(context.Background(), "child", RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatalf("positive child: %v", err)
	}
	defer assertTestClose(t, child.Close)
	if err := os.Rename(filepath.Join(path, "child"), filepath.Join(path, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = child.OpenDir(context.Background(), "grandchild", RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true})
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("replaced child: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "old", "grandchild")); !os.IsNotExist(err) {
		t.Fatal("mutated detached directory")
	}
}

func TestTrustedRoot_ClosedAndCanceledOperationsDoNotPublish(t *testing.T) {
	r, path := trustedTempCapability(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.WriteFile(ctx, "canceled", []byte("secret"), 0600, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile(context.Background(), "closed", []byte("secret"), 0600, nil); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	if f, _, err := r.OpenFile(context.Background(), "closed", 0600); f != nil || !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed read accepted")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled/closed mutation: %v %v", entries, err)
	}
}

type blockingTrustedBackend struct {
	nativeTrustedBackend
	entered, release chan struct{}
	once             sync.Once
}

func (b *blockingTrustedBackend) openFile(parent int, name string, flags int, mode uint32) (int, error) {
	if flags&unix.O_CREAT != 0 {
		b.once.Do(func() { close(b.entered); <-b.release })
	}
	return b.nativeTrustedBackend.openFile(parent, name, flags, mode)
}

func TestTrustedRoot_QueuedMutationCancellation(t *testing.T) {
	r, path := trustedTempCapability(t)
	b := &blockingTrustedBackend{entered: make(chan struct{}), release: make(chan struct{})}
	r.backend = b
	var release sync.Once
	defer release.Do(func() { close(b.release) })
	active := make(chan error, 1)
	go func() { active <- r.WriteFile(context.Background(), "active", []byte("first"), 0600, nil) }()
	<-b.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queued := make(chan error, 1)
	go func() { queued <- r.WriteFile(ctx, "queued", []byte("never"), 0600, nil) }()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued mutation ignored cancellation while another operation owned the descriptor")
	}
	release.Do(func() { close(b.release) })
	if err := <-active; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "queued")); !os.IsNotExist(err) {
		t.Fatal("canceled queued mutation published")
	}
}

type closingTrustedBackend struct {
	*blockingTrustedBackend
	closes atomic.Int32
}

func (b *closingTrustedBackend) close(fd int) error {
	b.closes.Add(1)
	return b.nativeTrustedBackend.close(fd)
}

func TestTrustedRoot_CloseWaitsForOwnerAndRejectsQueuedWork(t *testing.T) {
	r, path := trustedTempCapability(t)
	b := &closingTrustedBackend{blockingTrustedBackend: &blockingTrustedBackend{entered: make(chan struct{}), release: make(chan struct{})}}
	r.backend = b
	var release sync.Once
	defer release.Do(func() { close(b.release) })
	active := make(chan error, 1)
	go func() { active <- r.WriteFile(context.Background(), "active", []byte("complete"), 0600, nil) }()
	<-b.entered
	r.mu.Lock()
	closing := r.closing
	r.mu.Unlock()
	queued := make(chan error, 1)
	go func() { queued <- r.WriteFile(context.Background(), "queued-close", []byte("never"), 0600, nil) }()
	closed := make(chan error, 2)
	go func() { closed <- r.Close() }()
	go func() { closed <- r.Close() }()
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("Close did not mark closure while IO was active")
	}
	if err := r.WriteFile(context.Background(), "after-close", []byte("never"), 0600, nil); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("accepted post-closure work: %v", err)
	}
	select {
	case err := <-queued:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("queued close result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued operation was not released by Close")
	}
	if b.closes.Load() != 0 {
		t.Fatal("Close released/reused fd while active operation owned it")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before active operation finished")
	default:
	}
	release.Do(func() { close(b.release) })
	if err := <-active; err != nil {
		t.Fatalf("active operation failed during orderly Close: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if b.closes.Load() != 1 {
		t.Fatalf("closed %d root fds, want 1", b.closes.Load())
	}
	contents, err := os.ReadFile(filepath.Join(path, "active"))
	if err != nil || string(contents) != "complete" {
		t.Fatalf("active result %q: %v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(path, "after-close")); !os.IsNotExist(err) {
		t.Fatal("mutation after closure")
	}
}

func TestTrustedRoot_MoveAndRemoveRequireObservedIdentity(t *testing.T) {
	r, path := trustedTempCapability(t)
	ctx := context.Background()
	source, e := r.OpenDir(ctx, "source", RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = source.Close() }()
	target, e := r.OpenDir(ctx, "target", RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = target.Close() }()
	if e = source.WriteFile(ctx, "candidate", []byte("trusted binary"), 0700, nil); e != nil {
		t.Fatal(e)
	}
	f, id, e := source.OpenFile(ctx, "candidate", 0700)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	if e = source.MoveFileTo(ctx, "candidate", id, target, "binary", 0700, nil); e != nil {
		t.Fatal(e)
	}
	f, moved, e := target.OpenFile(ctx, "binary", 0700)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	if id != moved {
		t.Fatal("publication changed candidate inode")
	}
	if e = target.WriteFile(ctx, "binary", []byte("replacement"), 0700, &moved); e != nil {
		t.Fatal(e)
	}
	if e = target.RemoveFile(ctx, "binary", 0700, moved); !errors.Is(e, ErrIdentityMismatch) {
		t.Fatal("stale cleanup accepted")
	}
	bytes, e := os.ReadFile(filepath.Join(path, "target", "binary"))
	if e != nil || string(bytes) != "replacement" {
		t.Fatal("stale cleanup removed replacement")
	}
}

func TestTrustedRoot_ReadNamesUsesHeldDirectory(t *testing.T) {
	r, _ := trustedTempCapability(t)
	if err := r.WriteFile(context.Background(), "owned", []byte("bytes"), 0600, nil); err != nil {
		t.Fatal(err)
	}
	names, err := r.ReadNames(context.Background())
	if err != nil || len(names) != 1 || names[0] != "owned" {
		t.Fatalf("held directory names: %v %v", names, err)
	}
}

func TestTrustedRoot_RemoveEmptyDirKeepsNonemptyChild(t *testing.T) {
	r, _ := trustedTempCapability(t)
	ctx := context.Background()
	child, err := r.OpenDir(ctx, "stage", RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Close(); err != nil {
		t.Fatal(err)
	}
	if err = r.RemoveEmptyDir(ctx, "stage"); err != nil {
		t.Fatal(err)
	}
	names, err := r.ReadNames(ctx)
	if err != nil || len(names) != 0 {
		t.Fatalf("empty owned directory retained: %v %v", names, err)
	}
}

func TestTrustedRoot_DirectoryCleanupRejectsNonemptyAndCancellation(t *testing.T) {
	r, _ := trustedTempCapability(t)
	ctx := context.Background()
	child, err := r.OpenDir(ctx, "retained", RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = child.WriteFile(ctx, "resource", []byte("old"), 0600, nil); err != nil {
		t.Fatal(err)
	}
	if err = child.Close(); err != nil {
		t.Fatal(err)
	}
	if err = r.RemoveEmptyDir(ctx, "retained"); err == nil {
		t.Fatal("nonempty child removed")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = r.ReadNames(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled enumeration: %v", err)
	}
	if err = r.RemoveEmptyDir(canceled, "retained"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled removal: %v", err)
	}
}

type serviceExchangeSyncFailure struct {
	trustedBackend
	failure error
}

func (b serviceExchangeSyncFailure) sync(int) error { return b.failure }
func TestTrustedRoot_ServiceExchangeCommittedOnSyncFailure(t *testing.T) {
	root, _ := trustedTempCapability(t)
	ctx := context.Background()
	if err := root.WriteServiceEntry(ctx, "unit", []byte("original unit"), "", 0644, ServiceEntry{}); err != nil {
		t.Fatal(err)
	}
	stage, err := root.OpenDir(ctx, "stage", RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := stage.WriteServiceEntry(ctx, "candidate", nil, "/dev/null", 0644, ServiceEntry{}); err != nil {
		t.Fatal(err)
	}
	original, err := root.ReadServiceEntry(ctx, "unit")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := stage.ReadServiceEntry(ctx, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("fixture parent sync failed")
	backend := stage.backend
	stage.backend = serviceExchangeSyncFailure{trustedBackend: backend, failure: failure}
	published, err := stage.ExchangeServiceEntryWith(ctx, "candidate", candidate, root, "unit", original)
	if !published || !errors.Is(err, failure) {
		t.Fatalf("exchange publication=%v err=%v", published, err)
	}
	live, err := root.ReadServiceEntry(ctx, "unit")
	if err != nil {
		t.Fatal(err)
	}
	retained, err := stage.ReadServiceEntry(ctx, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if live.Key() != candidate.Key() || retained.Key() != original.Key() {
		t.Fatal("exchange did not preserve both recorded inodes")
	}
	stage.backend = backend
	published, err = stage.ExchangeServiceEntryWith(ctx, "candidate", retained, root, "unit", live)
	if !published || err != nil {
		t.Fatal("cannot restore retained original", published, err)
	}
	restored, err := root.ReadServiceEntry(ctx, "unit")
	if err != nil || restored.Key() != original.Key() {
		t.Fatal("restoration lost original identity", err)
	}
}
