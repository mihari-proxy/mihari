package platform

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// InstallRoot returns the machine-local directory that holds the OS-managed
// mihari binary (separate from the data root and from any download location).
// Override with MIHARI_INSTALL_ROOT (absolute preferred; relative is Abs'd by callers).
func InstallRoot() string {
	if root := os.Getenv("MIHARI_INSTALL_ROOT"); root != "" {
		return root
	}
	return defaultInstallRoot()
}

// InstalledBinaryName is mihari.exe on Windows and mihari elsewhere.
func InstalledBinaryName() string {
	if runtime.GOOS == "windows" {
		return "mihari.exe"
	}
	return "mihari"
}

// InstalledBinaryPath is InstallRoot()/InstalledBinaryName().
func InstalledBinaryPath() string {
	return filepath.Join(InstallRoot(), InstalledBinaryName())
}

// AbsoluteInstallRoot returns InstallRoot as a cleaned absolute path.
func AbsoluteInstallRoot() (string, error) {
	root := InstallRoot()
	if filepath.IsAbs(root) {
		return filepath.Clean(root), nil
	}
	return filepath.Abs(root)
}

// AbsoluteInstalledBinaryPath returns the absolute path of the installed binary.
func AbsoluteInstalledBinaryPath() (string, error) {
	root, err := AbsoluteInstallRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, InstalledBinaryName()), nil
}

// StageInstalledBinary copies source into the install directory and returns the
// absolute installed path. The OS service ImagePath must always use this path,
// never a download folder or developer workspace path.
//
// If source already resolves to the install path, no copy is performed.
func StageInstalledBinary(source string) (string, error) {
	if source == "" {
		return "", fmt.Errorf("source executable path is empty")
	}
	src, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve source executable: %w", err)
	}
	src, err = filepath.EvalSymlinks(src)
	if err != nil {
		// Symlink resolution can fail on some Windows setups; keep Abs path.
		src, _ = filepath.Abs(source)
	}

	dest, err := AbsoluteInstalledBinaryPath()
	if err != nil {
		return "", fmt.Errorf("resolve install path: %w", err)
	}
	destClean := filepath.Clean(dest)
	srcClean := filepath.Clean(src)
	if sameFilePath(srcClean, destClean) {
		return destClean, nil
	}

	if err := os.MkdirAll(filepath.Dir(destClean), 0o755); err != nil {
		return "", fmt.Errorf("create install directory: %w", err)
	}
	if err := copyFileReplace(srcClean, destClean); err != nil {
		return "", fmt.Errorf("copy mihari into install directory: %w", err)
	}
	return destClean, nil
}

func sameFilePath(a, b string) bool {
	if a == b {
		return true
	}
	// Resolve symlinks and Windows 8.3 short names (e.g. RUNNER~1 vs the real
	// long name) so the same file spelled differently still compares equal.
	// EvalSymlinks fails for paths that do not exist yet; keep the given path.
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	if a == b {
		return true
	}
	// Windows paths are case-insensitive for service ImagePath identity.
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return false
}

func copyFileReplace(src, dst string) error {
	// Defensive: never rename a file over itself (Windows denies it).
	if sameFilePath(src, dst) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o755
	}
	// Ensure execute bit on Unix for the installed binary.
	if runtime.GOOS != "windows" {
		mode |= 0o111
	}

	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".mihari-install-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup on failure.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// On Windows, Replace may fail if dst is locked by a running service.
	if err := os.Rename(tmpName, dst); err != nil {
		// Fallback: remove dst then rename (still fails if file is locked).
		_ = os.Remove(dst)
		if err2 := os.Rename(tmpName, dst); err2 != nil {
			return err
		}
	}
	return nil
}
