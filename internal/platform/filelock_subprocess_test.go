package platform

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const filelockChildEnv = "MIHARI_FILELOCK_CHILD"

func init() {
	if os.Getenv(filelockChildEnv) == "1" {
		os.Exit(runFilelockChild())
	}
}

func TestAdvisoryLock_SubprocessExitReleases(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(paths.LogDir, "mihari-tui.log.lock")
	stdin, _, cmd, errBuf := startFilelockChild(t, paths.Root, path, LockExclusive)

	lock := openAdvisoryLockAt(t, fs, path)
	waitForWaiter, tick := installLockTicks(t)
	acquired := make(chan error, 1)
	go func() {
		acquired <- lock.Lock(context.Background(), LockExclusive)
	}()
	waitForWaiter(t, acquired)

	if _, err := io.WriteString(stdin, "exit\n"); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child wait: %v stderr=%s", err, errBuf.String())
	}
	cmd.Process = nil
	tick()
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
}

func startFilelockChild(t *testing.T, root, path string, mode LockMode) (io.WriteCloser, *bufio.Scanner, *exec.Cmd, *bytes.Buffer) {
	t.Helper()
	modeName := "exclusive"
	if mode == LockShared {
		modeName = "shared"
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		filelockChildEnv+"=1",
		"MIHARI_FILELOCK_ROOT="+root,
		"MIHARI_FILELOCK_PATH="+path,
		"MIHARI_FILELOCK_MODE="+modeName,
	)
	errBuf := &bytes.Buffer{}
	cmd.Stderr = errBuf
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	sc := bufio.NewScanner(stdout)
	if !sc.Scan() || sc.Text() != "locked" {
		_ = cmd.Wait()
		cmd.Process = nil
		t.Fatalf("child locked line=%q stderr=%s scan=%v", sc.Text(), errBuf.String(), sc.Err())
	}
	return stdin, sc, cmd, errBuf
}

func runFilelockChild() int {
	root := os.Getenv("MIHARI_FILELOCK_ROOT")
	path := os.Getenv("MIHARI_FILELOCK_PATH")
	mode := LockExclusive
	if os.Getenv("MIHARI_FILELOCK_MODE") == "shared" {
		mode = LockShared
	}
	fs, err := NewPrivateFS(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "child NewPrivateFS: %v\n", err)
		return 1
	}
	defer func() { _ = fs.Close() }()
	lock, err := OpenAdvisoryLock(fs, path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "child OpenAdvisoryLock: %v\n", err)
		return 1
	}
	if err := lock.Lock(context.Background(), mode); err != nil {
		fmt.Fprintf(os.Stderr, "child Lock: %v\n", err)
		return 1
	}
	fmt.Println("locked")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		switch sc.Text() {
		case "exit":
			return 0
		case "unlock":
			if err := lock.Unlock(); err != nil {
				fmt.Fprintf(os.Stderr, "child Unlock: %v\n", err)
				return 1
			}
			fmt.Println("unlocked")
		case "close":
			if err := lock.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "child Close: %v\n", err)
				return 1
			}
			fmt.Println("closed")
		default:
			fmt.Fprintf(os.Stderr, "child unknown command %q\n", sc.Text())
			return 1
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "child stdin: %v\n", err)
		return 1
	}
	return 0
}
