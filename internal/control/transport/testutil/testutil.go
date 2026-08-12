package testutil

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func Endpoint(t testing.TB) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			t.Fatal(err)
		}
		return `\\.\pipe\mihari-test-` + hex.EncodeToString(suffix[:])
	}
	// Darwin (and some other Unixes) cap AF_UNIX path length near 104 bytes.
	// t.TempDir() under /var/folders/... is often too long for control.sock.
	dir, err := os.MkdirTemp("/tmp", "mh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "c.sock")
}
