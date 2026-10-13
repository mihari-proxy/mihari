//go:build linux

package ui

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
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

func TestRunUserClipboard_CancellationStopsCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runUserClipboardContext(ctx, []string{"/bin/sh", "-c", "exec sleep 60"}, "fixture")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestRunUserClipboard_DeadlineStopsRunningCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runUserClipboardContext(ctx, []string{"/bin/sh", "-c", "exec sleep 60"}, "fixture") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clipboard command outlived its deadline")
	}
}

func TestLinuxClipboardCommand_PreservesBackendAndSelection(t *testing.T) {
	cases := []struct {
		name    string
		wayland bool
		tools   []string
		want    []string
	}{
		{"wayland", true, []string{"wl-copy", "wl-paste", "xclip"}, []string{"/fixture/wl-copy"}},
		{"xclip", false, []string{"wl-copy", "wl-paste", "xclip"}, []string{"/fixture/xclip", "-in", "-selection", "clipboard"}},
		{"xsel", true, []string{"xsel"}, []string{"/fixture/xsel", "--input", "--clipboard"}},
		{"termux", false, []string{"termux-clipboard-set", "termux-clipboard-get"}, []string{"/fixture/termux-clipboard-set"}},
		{"wsl", false, []string{"clip.exe", "powershell.exe"}, []string{"/fixture/clip.exe"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(name string) (string, error) {
				for _, tool := range tc.tools {
					if name == tool {
						return "/fixture/" + name, nil
					}
				}
				return "", os.ErrNotExist
			}
			got, err := linuxClipboardCommand(tc.wayland, lookup)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("argv=%v err=%v", got, err)
			}
		})
	}
	if _, err := linuxClipboardCommand(false, func(string) (string, error) { return "", os.ErrNotExist }); err == nil {
		t.Fatal("missing tools accepted")
	}
}

func TestRunUserClipboard_CancellationKillsCommandChildren(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "ready")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	ready, err := os.OpenFile(fifo, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runUserClipboardContext(ctx, []string{"/bin/sh", "-c", `sleep 60 & echo $! > "$1"; wait`, "fixture", fifo}, "fixture")
	}()
	pidResult := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(ready).ReadString('\n'); pidResult <- strings.TrimSpace(line) }()
	var pid int
	select {
	case line := <-pidResult:
		pid, err = strconv.Atoi(line)
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("child did not signal readiness")
	}
	childExited := false
	t.Cleanup(func() {
		if !childExited {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clipboard parent did not stop")
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		name, _, readErr := readProcIdentity(pid)
		if errors.Is(readErr, os.ErrNotExist) {
			childExited = true
			return
		}
		raw, readErr := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if errors.Is(readErr, os.ErrNotExist) {
			childExited = true
			return
		}
		close := strings.LastIndexByte(string(raw), ')')
		if close >= 0 && strings.HasPrefix(string(raw)[close+1:], " Z ") {
			childExited = true
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("clipboard child still alive: pid=%d name=%q", pid, name)
		}
	}
}
