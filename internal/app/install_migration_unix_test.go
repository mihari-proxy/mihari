//go:build linux || darwin

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestUnixMigration_TrustedCapStatIdentityAndMtime(t *testing.T) {
	ctx := context.Background()
	root := migrationTrustedTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "mihari.yaml"), []byte("same-bytes"), 0o600); err != nil {
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
	entry, err := cap.Stat(ctx, "mihari.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Dev == "" || entry.Ino == "" || entry.Mtime == 0 || entry.Ctime == 0 {
		t.Fatalf("trustedCap.Stat missing identity/times: %+v", entry)
	}
	obs := map[string]sourceObservation{"mihari.yaml": rememberObservation("mihari.yaml", entry.Hash, entry)}
	if err := verifyStationary(ctx, cap, obs); err != nil {
		t.Fatalf("stable identity should pass: %v", err)
	}
	path := filepath.Join(root, "mihari.yaml")
	if err := os.WriteFile(path, []byte("same-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := time.Unix(0, entry.Mtime).Add(time.Second)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	err = verifyStationary(ctx, cap, obs)
	if err == nil || apiCode(err) != protocol.CodeRevisionConflict {
		t.Fatalf("inode/mtime rewrite: want revision_conflict, got %v", err)
	}
}

func migrationTrustedTempDir(t *testing.T) string {
	t.Helper()
	// The capability rejects writable ancestors such as /tmp. Keep this
	// disposable fixture in the checkout and make its application mode exact.
	root, err := os.MkdirTemp(".", "mihari-migration-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestUnixMigration_BootstrapResidueReadOnlySource(t *testing.T) {
	fx := bootstrapMigrationFixture(t)
	ctx := context.Background()
	source, err := openReadOnlyMigrationRoot(ctx, fx.source.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	opts := fx.options()
	opts.Source = source
	prepared, err := prepareMigration(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	if err := prepared.recheckAndPublish(ctx); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, fx.source.osPath("transactions/"+testTxnID+"/transaction-id"), []byte(testTxnID))
	if err := prepared.recheckAndPublish(ctx); err == nil || apiCode(err) != protocol.CodeRevisionConflict {
		t.Fatalf("new transaction accepted after source observation: %v", err)
	}
}
