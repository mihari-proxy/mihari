//go:build linux || darwin

package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlySource_UserWritableAncestry(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0777); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "legacy")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "mihomo")
	if err := os.WriteFile(file, []byte("untrusted core"), 0755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := OpenReadOnlySource(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	entry, raw, err := source.Read(ctx, "mihomo", 128)
	if err != nil || entry.Kind != "file" || string(raw) != "untrusted core" || entry.SHA256 == "" || entry.Mount == "" {
		t.Fatalf("read-only legacy source rejected: %+v %v", entry, err)
	}
	if err := os.WriteFile(file, []byte("changed source"), 0755); err != nil {
		t.Fatal(err)
	}
	next, _, err := source.Read(ctx, "mihomo", 128)
	if err != nil {
		t.Fatal(err)
	}
	if next.SHA256 == entry.SHA256 {
		t.Fatal("concurrent business change lost")
	}
	if _, err := OpenTrustedRoot(ctx, root, RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700}); err == nil {
		t.Fatal("read-only exception weakened privileged target policy")
	}
}
func TestReadOnlySource_RejectsLinksAndAnchorReplacement(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "source")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plain"), []byte("bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := OpenReadOnlySource(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := os.Symlink("plain", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := source.Read(ctx, "alias", 128); err == nil {
		t.Fatal("followed source symlink")
	}
	if err := os.Link(filepath.Join(root, "plain"), filepath.Join(root, "hard")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := source.Read(ctx, "hard", 128); err == nil {
		t.Fatal("accepted hardlinked source")
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := source.Snapshot(ctx); err == nil {
		t.Fatal("accepted replaced source anchor")
	}
}

type sourceReplaceOnOpen struct {
	trustedBackend
	replace func()
}

func (b sourceReplaceOnOpen) openFile(parent int, name string, flags int, mode uint32) (int, error) {
	fd, err := b.trustedBackend.openFile(parent, name, flags, mode)
	if err == nil {
		b.replace()
	}
	return fd, err
}
func TestReadOnlySource_RejectsDescendantReplacementDuringRead(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "subscriptions")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "catalog.json"), []byte("catalog"), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := OpenReadOnlySource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	source.backend = sourceReplaceOnOpen{trustedBackend: source.backend, replace: func() {
		if err := os.Rename(directory, directory+"-old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}}
	if _, _, err := source.Read(context.Background(), "subscriptions/catalog.json", 128); err == nil {
		t.Fatal("accepted read from detached descendant")
	}
}
