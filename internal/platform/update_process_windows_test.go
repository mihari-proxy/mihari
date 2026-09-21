//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const issue282UpdateProcessHelper = "MIHARI_ISSUE282_UPDATE_PROCESS_HELPER"

// TestIssue282UpdateProcessHelper is reached only through a copied test binary.
// The stubborn role has a safety deadline in case the parent test disappears.
func TestIssue282UpdateProcessHelper(t *testing.T) {
	role := os.Getenv(issue282UpdateProcessHelper)
	if role == "" {
		return
	}
	ready := os.Getenv("MIHARI_ISSUE282_UPDATE_PROCESS_READY")
	switch role {
	case "cooperative":
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		closeSignal, err := WindowsClientExitSignal(ctx, cancel)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := closeSignal(); err != nil {
				t.Error(err)
			}
		}()
		issue282WriteReadyPID(t, ready)
		<-ctx.Done()
	case "stubborn":
		issue282WriteReadyPID(t, ready)
		<-time.NewTimer(20 * time.Second).C
	default:
		t.Fatalf("unknown helper role %q", role)
	}
}

func issue282WriteReadyPID(t *testing.T, path string) {
	t.Helper()
	if path == "" {
		t.Fatal("ready marker path is empty")
	}
	if err := os.WriteFile(path+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}

func TestIssue282RestartManagerAndClientExitPrimitives(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	copyPath := filepath.Join(temporary, "issue282-update-client.exe")
	if err := issue282CopyExecutable(executable, copyPath); err != nil {
		t.Fatal(err)
	}

	t.Run("client event requests normal exit", func(t *testing.T) {
		child := issue282StartCopiedChild(t, copyPath, "cooperative")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := child.waitReady(ctx); err != nil {
			t.Fatal(err)
		}

		users := issue282WaitForOnlyBinaryUser(t, ctx, copyPath, uint32(child.cmd.Process.Pid))
		defer issue282CloseIdentities(t, users)
		issue282AssertCreationTime(t, users[0])

		event, err := users[0].OpenClientExitSignal()
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetEvent(event); err != nil {
			_ = windows.CloseHandle(event)
			t.Fatal(err)
		}
		if err := windows.CloseHandle(event); err != nil {
			t.Fatal(err)
		}
		if err := users[0].WaitExit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := child.wait(); err != nil {
			t.Fatalf("cooperative client exit: %v", err)
		}
	})

	t.Run("forced fallback confirms exit", func(t *testing.T) {
		child := issue282StartCopiedChild(t, copyPath, "stubborn")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := child.waitReady(ctx); err != nil {
			t.Fatal(err)
		}

		users := issue282WaitForOnlyBinaryUser(t, ctx, copyPath, uint32(child.cmd.Process.Pid))
		defer issue282CloseIdentities(t, users)
		issue282AssertCreationTime(t, users[0])
		if err := users[0].Terminate(ctx); err != nil {
			t.Fatal(err)
		}
		if exited, err := users[0].Exited(ctx); err != nil || !exited {
			t.Fatalf("forced client exit confirmation=%v err=%v", exited, err)
		}
		if err := child.wait(); err == nil {
			t.Fatal("terminated client unexpectedly exited successfully")
		} else if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("wait for terminated client: %v", err)
		}
	})
}

func issue282CopyExecutable(source, target string) (err error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	_, err = io.Copy(out, in)
	return err
}

type issue282CopiedChild struct {
	cmd       *exec.Cmd
	readyPath string
	waited    bool
}

func issue282StartCopiedChild(t *testing.T, executable, role string) *issue282CopiedChild {
	t.Helper()
	readyPath := filepath.Join(t.TempDir(), "ready.pid")
	cmd := exec.Command(executable, "-test.run=^TestIssue282UpdateProcessHelper$")
	cmd.Env = append(os.Environ(),
		issue282UpdateProcessHelper+"="+role,
		"MIHARI_ISSUE282_UPDATE_PROCESS_READY="+readyPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := &issue282CopiedChild{cmd: cmd, readyPath: readyPath}
	t.Cleanup(func() {
		if child.waited {
			return
		}
		if err := child.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("cleanup copied child kill: %v", err)
		}
		_ = child.cmd.Wait()
		child.waited = true
	})
	return child
}

func (c *issue282CopiedChild) waitReady(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		body, err := os.ReadFile(c.readyPath)
		if err == nil {
			pid, parseErr := strconv.Atoi(string(body))
			if parseErr != nil {
				return parseErr
			}
			if pid != c.cmd.Process.Pid {
				return fmt.Errorf("ready PID=%d, child PID=%d", pid, c.cmd.Process.Pid)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *issue282CopiedChild) wait() error {
	err := c.cmd.Wait()
	c.waited = true
	return err
}

func issue282WaitForOnlyBinaryUser(t *testing.T, ctx context.Context, path string, wantPID uint32) []*WindowsProcessIdentity {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		users, err := WindowsBinaryUsers(ctx, []string{path})
		if err != nil {
			t.Fatal(err)
		}
		if len(users) == 1 && users[0].Identity().PID == wantPID {
			return users
		}
		if len(users) > 0 {
			identities := make([]string, 0, len(users))
			for _, user := range users {
				id := user.Identity()
				identities = append(identities, fmt.Sprintf("%d:%s", id.PID, id.ImagePath))
			}
			issue282CloseIdentities(t, users)
			t.Fatalf("Restart Manager returned users other than the sole fixture child: %s", strings.Join(identities, ", "))
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func issue282AssertCreationTime(t *testing.T, process *WindowsProcessIdentity) {
	t.Helper()
	id := process.Identity()
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, id.PID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.CloseHandle(handle); err != nil {
			t.Error(err)
		}
	}()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	actual := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	if actual != id.CreationFiletime {
		t.Fatalf("creation filetime from retained identity=%d, direct process query=%d", id.CreationFiletime, actual)
	}
}

func issue282CloseIdentities(t *testing.T, identities []*WindowsProcessIdentity) {
	t.Helper()
	for _, identity := range identities {
		if err := identity.Close(); err != nil {
			t.Error(err)
		}
	}
}
