//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyMigrationSource_OversizeHasMigrationClassification(t *testing.T) {
	root := migrationTrustedTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "resource"), []byte("oversized"), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := openReadOnlyMigrationRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := source.ReadFile(context.Background(), "resource", 3); !errors.Is(err, errMigrationOversize) {
		t.Fatalf("oversized resource classification: %v", err)
	}
}

func TestMigrationCapabilities_SameDirectoryHasMatchingIdentity(t *testing.T) {
	ctx := context.Background()
	root := migrationTrustedTempDir(t)
	source, err := openReadOnlyMigrationRoot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	}()
	target, err := openTrustedMigrationRoot(ctx, root, uint32(os.Geteuid()), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := target.Close(); err != nil {
			t.Error(err)
		}
	}()
	sDev, sIno, sMount := source.Identity()
	tDev, tIno, tMount := target.Identity()
	if sDev != tDev || sIno != tIno || sMount != tMount {
		t.Fatalf("same opened directory has different source/target identities: source=%s:%s@%s target=%s:%s@%s", sDev, sIno, sMount, tDev, tIno, tMount)
	}
}
