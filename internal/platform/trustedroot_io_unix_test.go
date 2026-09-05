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
		unix.Close(fd)
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
	defer child.Close()
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
	defer child.Close()
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
