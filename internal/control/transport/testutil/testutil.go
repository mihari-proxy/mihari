package testutil

import (
	"crypto/rand"
	"encoding/hex"
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
	return filepath.Join(t.TempDir(), "control.sock")
}
