//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type publicationSyncFailure struct {
	trustedBackend
	failure      error
	afterPublish func()
}

func (b publicationSyncFailure) sync(int) error {
	if b.afterPublish != nil {
		b.afterPublish()
	}
	return b.failure
}

func TestTrustedRoot_WriteFileReportsPublishedIdentityOnSyncFailure(t *testing.T) {
	root, path := trustedTempCapability(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("fixture publication sync failure")
	backend := root.backend
	root.backend = publicationSyncFailure{trustedBackend: backend, failure: failure, afterPublish: cancel}
	identity, err := root.WriteFileWithIdentity(ctx, "config.yaml", []byte("generated config"), 0600, nil)
	root.backend = backend
	content, readErr := os.ReadFile(filepath.Join(path, "config.yaml"))
	if readErr != nil || string(content) != "generated config" {
		t.Fatalf("fixture failed before publication: %v", readErr)
	}
	if identity == nil || !errors.Is(err, failure) {
		t.Fatalf("post-publication failure lost identity: identity=%v err=%v", identity, err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("fixture did not cancel after publication")
	}
	if err := root.RemoveFile(context.WithoutCancel(ctx), "config.yaml", 0600, *identity); err != nil {
		t.Fatalf("published identity cannot authorize cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-publication cleanup left a file: %v", err)
	}
}

func TestTrustedRoot_WriteFilePublishedIdentityCannotRemoveReplacement(t *testing.T) {
	root, path := trustedTempCapability(t)
	ctx := context.Background()
	failure := errors.New("fixture publication sync failure")
	backend := root.backend
	root.backend = publicationSyncFailure{trustedBackend: backend, failure: failure, afterPublish: func() {
		if err := os.Rename(filepath.Join(path, "config.yaml"), filepath.Join(path, "retained.yaml")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "config.yaml"), []byte("foreign replacement"), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	identity, err := root.WriteFileWithIdentity(ctx, "config.yaml", []byte("generated config"), 0600, nil)
	root.backend = backend
	if identity == nil || !errors.Is(err, failure) {
		t.Fatalf("post-publication failure lost identity: identity=%v err=%v", identity, err)
	}
	if err := root.RemoveFile(ctx, "config.yaml", 0600, *identity); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("cleanup authorized a foreign replacement: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(path, "config.yaml"))
	if err != nil || string(content) != "foreign replacement" {
		t.Fatalf("cleanup changed foreign file: %v", err)
	}
}

func TestTrustedRoot_WriteFileCollisionHasNoPublishedIdentity(t *testing.T) {
	root, path := trustedTempCapability(t)
	ctx := context.Background()
	if err := root.WriteFile(ctx, "config.yaml", []byte("existing config"), 0600, nil); err != nil {
		t.Fatal(err)
	}
	identity, err := root.WriteFileWithIdentity(ctx, "config.yaml", []byte("new config"), 0600, nil)
	if identity != nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("failed no-replace write claimed an identity: identity=%v err=%v", identity, err)
	}
	content, err := os.ReadFile(filepath.Join(path, "config.yaml"))
	if err != nil || string(content) != "existing config" {
		t.Fatalf("failed no-replace write changed existing file: %v", err)
	}
}
