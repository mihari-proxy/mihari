package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
)

func TestUninstallChecker_AcceptsExistingUnixServiceJournalArtifacts(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("a", 32)
	for _, name := range []string{
		"install-transaction.json",
		"transactions/" + id + "/transaction-id",
		"transactions/" + id + "/unit",
		"transactions/" + id + "/unit-bootstrap",
		"transactions/" + id + "/ready.json",
		"transactions/" + id + "/validation-launch.json",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := app.CheckUninstallFiles(context.Background(), []app.UninstallTarget{{Path: root, Kind: "base"}}); err != nil {
		t.Fatal(err)
	}
}
