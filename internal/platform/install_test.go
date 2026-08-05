package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstalledBinaryPathUsesInstallRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MIHARI_INSTALL_ROOT", root)
	wantName := "mihari"
	if runtime.GOOS == "windows" {
		wantName = "mihari.exe"
	}
	got := InstalledBinaryPath()
	if got != filepath.Join(root, wantName) {
		t.Fatalf("InstalledBinaryPath=%q want under %q", got, root)
	}
}

func TestStageInstalledBinaryCopiesAwayFromSource(t *testing.T) {
	installRoot := t.TempDir()
	t.Setenv("MIHARI_INSTALL_ROOT", installRoot)

	srcDir := t.TempDir()
	srcName := "mihari-download"
	if runtime.GOOS == "windows" {
		srcName = "mihari-download.exe"
	}
	src := filepath.Join(srcDir, srcName)
	payload := []byte("fake-mihari-binary-v1")
	if err := os.WriteFile(src, payload, 0o755); err != nil {
		t.Fatal(err)
	}

	dest, err := StageInstalledBinary(src)
	if err != nil {
		t.Fatal(err)
	}
	wantDest, err := AbsoluteInstalledBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	if dest != wantDest {
		t.Fatalf("dest=%q want=%q", dest, wantDest)
	}
	if dest == src {
		t.Fatal("installed path must not equal download/source path")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("installed content mismatch")
	}
	// Source tree must remain untouched as the service ImagePath target.
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source should remain: %v", err)
	}
}

func TestStageInstalledBinaryIdempotentWhenAlreadyInstalled(t *testing.T) {
	installRoot := t.TempDir()
	t.Setenv("MIHARI_INSTALL_ROOT", installRoot)
	dest, err := AbsoluteInstalledBinaryPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := StageInstalledBinary(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got != dest {
		t.Fatalf("got=%q want=%q", got, dest)
	}
}
