//go:build linux || darwin

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeDefinitionStore_RestoresOriginalBytesAndMode(t *testing.T) {
	if os.Getenv("MIHARI_NATIVE_INSTALL_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("isolated native root fixture is not enabled")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "mihari.service")
	original := []byte("original unit")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store := osDefinitionStore{}
	old, err := store.Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	target := old
	target.Mode = 0644
	target.Bytes = []byte("target unit")
	if err := store.Write(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	actual, err := store.Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual.Bytes) != string(original) || actual.Mode != 0600 || actual.Identity == "" {
		t.Fatalf("original definition not restored: %+v", actual)
	}
	if err := store.Mask(context.Background(), path, "/dev/null"); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(path)
	if err != nil || link != "/dev/null" {
		t.Fatalf("actual mask=%q err=%v", link, err)
	}
	if err := store.Write(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	names, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatal("private publication temporaries leaked")
	}
}
