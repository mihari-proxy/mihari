//go:build linux || darwin

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestUnixMigration_TrustedCapStatIdentityAndMtime(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload.txt"), []byte("same-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	cap, err := openTrustedMigrationRoot(ctx, root, uint32(os.Geteuid()), false)
	if err != nil {
		t.Fatalf("open trusted root: %v", err)
	}
	t.Cleanup(func() {
		if err := cap.Close(); err != nil {
			t.Error(err)
		}
	})
	entry, err := cap.Stat(ctx, "payload.txt")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Dev == "" || entry.Ino == "" || entry.Mtime == 0 || entry.Ctime == 0 {
		t.Fatalf("trustedCap.Stat missing identity/times: %+v", entry)
	}
	obs := map[string]sourceObservation{"payload.txt": rememberObservation("payload.txt", entry.Hash, entry)}
	if err := verifyStationary(ctx, cap, obs); err != nil {
		t.Fatalf("stable identity should pass: %v", err)
	}
	path := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(path, []byte("same-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = verifyStationary(ctx, cap, obs)
	if err == nil || apiCode(err) != protocol.CodeRevisionConflict {
		t.Fatalf("inode/mtime rewrite: want revision_conflict, got %v", err)
	}
}
