package panel

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestInstallCandidateExtractsToStagingThenPromotes(t *testing.T) {
	root := t.TempDir()
	paths := platform.NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	archivePath := writePanelZip(t, map[string]string{
		"dist/index.html": "<html>zashboard</html>",
		"dist/app.js":     "1",
	})

	installed, err := InstallFromZip(InstallRequest{
		PanelID:    IDZashboard,
		Build:      "v2.1.0",
		Archive:    archivePath,
		StagingDir: paths.PanelStaging,
		WebRoot:    paths.WebRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(paths.WebRoot, IDZashboard, "v2.1.0")
	if installed != wantDir {
		t.Fatalf("installed=%q want=%q", installed, wantDir)
	}
	raw, err := os.ReadFile(filepath.Join(wantDir, "dist", "index.html"))
	if err != nil || string(raw) != "<html>zashboard</html>" {
		t.Fatalf("content=%q err=%v", raw, err)
	}
	// Staging should not leave a permanent incomplete tree for this build.
	stagingCandidate := filepath.Join(paths.PanelStaging, IDZashboard+"-v2.1.0")
	if _, err := os.Stat(stagingCandidate); !os.IsNotExist(err) {
		t.Fatalf("staging candidate should be removed after promote, err=%v", err)
	}
}

func TestInstallCandidateRejectsUnsafeArchive(t *testing.T) {
	root := t.TempDir()
	paths := platform.NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	archivePath := writePanelZip(t, map[string]string{
		"../escape.txt": "nope",
		"index.html":    "<html>ok</html>",
	})
	_, err := InstallFromZip(InstallRequest{
		PanelID:    IDZashboard,
		Build:      "bad",
		Archive:    archivePath,
		StagingDir: paths.PanelStaging,
		WebRoot:    paths.WebRoot,
	})
	if err == nil {
		t.Fatal("expected unsafe archive rejection")
	}
	if _, err := os.Stat(filepath.Join(paths.WebRoot, IDZashboard, "bad")); err == nil {
		t.Fatal("unsafe archive must not promote into web root")
	}
}

func TestInstallCandidateRequiresPanelAndBuild(t *testing.T) {
	root := t.TempDir()
	paths := platform.NewPaths(root)
	archivePath := writePanelZip(t, map[string]string{"index.html": "<html>ok</html>"})
	_, err := InstallFromZip(InstallRequest{
		Build:      "v1",
		Archive:    archivePath,
		StagingDir: paths.PanelStaging,
		WebRoot:    paths.WebRoot,
	})
	if err == nil {
		t.Fatal("expected missing panel id rejection")
	}
}

func TestPanelBuildDir(t *testing.T) {
	got := PanelBuildDir("/data/web", IDMetaCubeXD, "8e31c4a")
	want := filepath.Join("/data/web", IDMetaCubeXD, "8e31c4a")
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func writePanelZip(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "panel.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
