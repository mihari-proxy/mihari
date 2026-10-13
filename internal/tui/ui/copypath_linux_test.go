//go:build linux

package ui

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWaylandDisplay_PrefersSessionAndRejectsAmbiguity(t *testing.T) {
	dir := t.TempDir()
	one, err := net.Listen("unix", filepath.Join(dir, "wayland-0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = one.Close() })
	two, err := net.Listen("unix", filepath.Join(dir, "wayland-1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = two.Close() })
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	if got := waylandDisplay(dir); got != "wayland-1" {
		t.Fatalf("session ignored: %q", got)
	}
	t.Setenv("WAYLAND_DISPLAY", filepath.Join(dir, "wayland-1"))
	if got := waylandDisplay(dir); got != "wayland-1" {
		t.Fatalf("absolute session path ignored: %q", got)
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	if got := waylandDisplay(dir); got != "" {
		t.Fatalf("ambiguous session selected: %q", got)
	}
	if err := two.Close(); err != nil {
		t.Fatal(err)
	}
	if got := waylandDisplay(dir); got != "wayland-0" {
		t.Fatalf("unique socket missed: %q", got)
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-99")
	if got := waylandDisplay(dir); got != "wayland-0" {
		t.Fatalf("unique fallback missed: %q", got)
	}
}

func TestRunUserClipboard_ReturnsWhileChildHoldsStderr(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hold.sh")
	got := filepath.Join(dir, "stdin.txt")
	body := "#!/bin/sh\ncat >\"$1\"\n(sleep 5) &\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- runUserClipboard([]string{"/bin/sh", script, got}, "fixture-path")
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard command blocked after the parent exited")
	}
	raw, err := os.ReadFile(got)
	if err != nil || string(raw) != "fixture-path" {
		t.Fatalf("stdin=%q err=%v", raw, err)
	}
}

func TestRunUserClipboard_FailureKeepsStderr(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	body := "#!/bin/sh\ncat >/dev/null\necho 'wl-copy: fixture unavailable' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	err := runUserClipboard([]string{"/bin/sh", script}, "fixture")
	if err == nil || !strings.Contains(err.Error(), "wl-copy: fixture unavailable") {
		t.Fatalf("err=%v", err)
	}
}
