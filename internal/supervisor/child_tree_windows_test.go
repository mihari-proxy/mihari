//go:build windows

package supervisor

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/core"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestWindowsChild_ProvidesOwnedTreeWait requires the native starter to own descendant exit.
func TestWindowsChild_ProvidesOwnedTreeWait(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(commandHelperEnv, "partial-stdout")
	child, err := (CommandStarter{BinaryPath: exe}).Start()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	t.Cleanup(func() {
		_ = child.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("child wait did not finish")
		}
	})
	waiter, ok := child.(interface{ WaitDescendants(context.Context) error })
	if !ok {
		t.Fatal("Windows starter cannot confirm descendant exit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waiter.WaitDescendants(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestWindowsTreeHelper runs only as a disposable process in tree lifecycle tests.
func TestWindowsTreeHelper(t *testing.T) {
	mode := os.Getenv("MIHARI_TEST_JOB_ROLE")
	if mode == "" {
		return
	}
	if mode == "leaf" {
		<-time.NewTimer(15 * time.Second).C
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestWindowsTreeHelper$")
	cmd.Env = append(os.Environ(), "MIHARI_TEST_JOB_ROLE=leaf")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	marker := os.Getenv("MIHARI_TEST_JOB_MARKER")
	if err = os.WriteFile(marker+".tmp", []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err == nil {
		err = os.Rename(marker+".tmp", marker)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	if mode == "parent-exit" {
		return
	} // Job owner must collect the child after direct-parent exit.
	<-time.NewTimer(15 * time.Second).C
	_ = cmd.Process.Kill()
	_ = cmd.Wait() // Fixture deadline if the test runner disappears.
}

// TestWindowsChild_CollectsImmediateDescendant uses the real starter for both termination and natural-parent exit.
func TestWindowsChild_CollectsImmediateDescendant(t *testing.T) {
	for _, mode := range []string{"parent-hold", "parent-exit"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "leaf.pid")
			starter := CommandStarter{Stdout: io.Discard, Stderr: io.Discard, CommandFactory: func(context.Context) (core.CoreCommand, func() error, error) {
				return core.CoreCommand{Binary: exe, Args: []string{"-test.run=^TestWindowsTreeHelper$"}, Env: append(os.Environ(), "MIHARI_TEST_JOB_ROLE="+mode, "MIHARI_TEST_JOB_MARKER="+marker)}, func() error { return nil }, nil
			}}
			child, err := starter.Start()
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- child.Wait() }()
			waited := false
			t.Cleanup(func() {
				_ = child.Kill()
				if !waited {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("fixture wait timeout")
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var pid uint64
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				body, e := os.ReadFile(marker)
				if e == nil {
					pid, e = strconv.ParseUint(string(body), 10, 32)
					if e != nil {
						t.Fatal(e)
					}
					break
				}
				if !errors.Is(e, os.ErrNotExist) && !errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
					t.Fatal(e)
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-ticker.C:
				}
			}
			// Open only the PID produced by our own fixture. The production Job identity
			// remains the termination authority; this handle merely observes exit.
			leaf, openErr := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
			if openErr != nil && !errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
				t.Fatal(openErr)
			}
			if leaf != 0 {
				defer func() {
					if err := windows.CloseHandle(leaf); err != nil {
						t.Error(err)
					}
				}()
			}
			if mode == "parent-hold" {
				if err = child.Terminate(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err = <-done:
				waited = true
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if mode == "parent-exit" && err != nil {
				t.Fatalf("natural parent exit: %v", err)
			}
			if err = child.(interface{ WaitDescendants(context.Context) error }).WaitDescendants(ctx); err != nil {
				t.Fatal(err)
			}
			if leaf != 0 {
				// Job accounting can reach zero before the final process handle
				// becomes signaled; observe kernel completion with a bounded wait.
				state, e := windows.WaitForSingleObject(leaf, 3000)
				if e != nil || state != windows.WAIT_OBJECT_0 {
					t.Fatalf("leaf survived: state=%d err=%v", state, e)
				}
			}
		})
	}
}
