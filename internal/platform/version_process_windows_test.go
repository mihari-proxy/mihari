package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsVersionProcess_Native(t *testing.T) {
	// Hosted runners with UAC disabled cannot test a filtered token, but can
	// still exercise this process API with an inert fixture and their own token.
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("native secondary-logon API requires the existing admin impersonation privilege")
	}
	var token windows.Token
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &source); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := windows.DuplicateTokenEx(source, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_SESSIONID, nil, windows.SecurityImpersonation, windows.TokenPrimary, &token); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := token.Close(); err != nil {
			t.Error(err)
		}
	})
	exe := filepath.Join(t.TempDir(), "version fixture.exe")
	build := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags", "-X main.version=v1.2.3", "-o", exe, "../update/testdata/versionprobe")
	build.Env = append(os.Environ(), "GOTOOLCHAIN="+runtime.Version())
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, raw)
	}
	for _, mode := range []string{"version", "stdout-overflow", "stderr-overflow", "wait", "missing", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "probe-mode"), []byte(mode), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.Command(exe, "self", "version", "--json")
			cmd.Dir = dir
			cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "WINDIR=" + os.Getenv("WINDIR"), "MIHARI_DATA=" + dir, "HOME=" + dir, "USERPROFILE=" + dir, "TEMP=" + dir, "TMP=" + dir}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			overflow := errors.New("bounded output exceeded")
			if mode == "stdout-overflow" {
				cmd.Stdout = versionOverflowWriter{cancel, overflow}
			}
			if mode == "stderr-overflow" {
				cmd.Stderr = versionOverflowWriter{cancel, overflow}
			}
			if mode == "missing" {
				cmd.Path = filepath.Join(dir, "missing.exe")
			}
			if mode == "canceled" {
				cancel()
			}
			err := runWindowsVersionWithToken(ctx, cmd, token)
			switch mode {
			case "version":
				if err != nil || !strings.Contains(stdout.String(), `"version":"v1.2.3"`) || stderr.Len() != 0 {
					t.Fatalf("stdout=%s stderr=%s error=%v", stdout.String(), stderr.String(), err)
				}
			case "wait":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("timeout=%v", err)
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation=%v", err)
				}
			case "missing":
				if err == nil {
					t.Fatal("missing binary accepted")
				}
			default:
				if !errors.Is(err, overflow) {
					t.Fatalf("overflow cause=%v", err)
				}
			}
		})
	}
}

type versionOverflowWriter struct {
	cancel context.CancelFunc
	err    error
}

func (w versionOverflowWriter) Write([]byte) (int, error) { w.cancel(); return 0, w.err }
