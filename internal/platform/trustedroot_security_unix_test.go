//go:build unix_security && (linux || darwin)

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func securityTrustedParent(t *testing.T) string {
	t.Helper()
	defaults := SystemLayoutDefaults()
	parent := os.Getenv("MIHARI_SECURITY_ROOT")
	if os.Geteuid() != 0 || defaults.BaseDir != filepath.Join(parent, "system") {
		t.Fatal("validated isolated root required")
	}
	return parent
}

func TestSecurityTrustedRootPositive(t *testing.T) {
	securityTrustedParent(t)
	root, err := OpenTrustedRoot(context.Background(), SystemLayoutDefaults().BaseDir, RootPolicy{Mode: 0711, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, root.Close)
	path, id, owner, mode, err := root.Snapshot(context.Background())
	if err != nil || path != SystemLayoutDefaults().BaseDir || id == "" || owner != 0 || mode != 0711 {
		t.Fatalf("native root snapshot: %s %v", id, err)
	}
	data, err := root.OpenDir(context.Background(), "data", RootPolicy{Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := data.Close(); err != nil {
		t.Fatal(err)
	}
	node := root.chain[len(root.chain)-1].node
	raw, err := json.Marshal(map[string]any{"uid": owner, "mode": mode, "dev": node.id.dev, "ino": node.id.ino, "mount": id})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("security_root=%s", raw)
}

func TestSecurityDirectoryIdentity(t *testing.T) {
	parent := securityTrustedParent(t)
	path := filepath.Join(parent, "identity-attack")
	root, err := OpenTrustedRoot(context.Background(), path, RootPolicy{Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, root.Close)
	_, before, _, _, err := root.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+"-retained"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := root.Snapshot(context.Background()); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("replaced identity accepted: %v", err)
	}
	replacement, err := OpenTrustedRoot(context.Background(), path, RootPolicy{Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, replacement.Close)
	_, after, _, _, err := replacement.Snapshot(context.Background())
	if err != nil || before == after {
		t.Fatal("replacement identity not distinguished")
	}
	t.Logf("security_identity before=%s after=%s", before, after)
}
