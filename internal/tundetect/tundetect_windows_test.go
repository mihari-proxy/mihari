//go:build windows

package tundetect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsProcessPathMatchesExecutable(t *testing.T) {
	got := windowsProcessPath(uint32(os.Getpid()))
	if got == "" {
		t.Fatal("empty image path")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(exe)) {
		t.Fatalf("got=%q want=%q", got, exe)
	}
}
