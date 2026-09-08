//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedRoot_SameParentMoveRetainsIdentityAndCapability(t *testing.T) {
	r, path := trustedTempCapability(t)
	ctx := context.Background()
	if err := r.WriteFile(ctx, "mihari.yaml", []byte("old settings"), 0600, nil); err != nil {
		t.Fatal(err)
	}
	f, id, err := r.OpenFile(ctx, "mihari.yaml", 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = r.MoveFileTo(ctx, "mihari.yaml", id, r, "mihari.yaml.old", 0600, nil); err != nil {
		t.Fatalf("same-parent WAL backup failed: %v", err)
	}
	f, moved, err := r.OpenFile(ctx, "mihari.yaml.old", 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if moved != id {
		t.Fatal("backup changed sealed inode")
	}
	if err = r.WriteFile(ctx, "mihari.yaml", []byte("new settings"), 0600, nil); err != nil {
		t.Fatal(err)
	}
	if err = r.MoveFileTo(ctx, "mihari.yaml.old", moved, r, "mihari.yaml", 0600, &id); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatal("stale destination identity accepted")
	}
	b, err := os.ReadFile(filepath.Join(path, "mihari.yaml"))
	if err != nil || string(b) != "new settings" {
		t.Fatal("stale restore changed new settings")
	}
}
