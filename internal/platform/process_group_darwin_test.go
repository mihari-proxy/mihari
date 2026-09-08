//go:build darwin

package platform

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

const processGroupHelperEnv = "MIHARI_PROCESS_GROUP_HELPER"

func TestDarwinGroupHasPeers_IsolatedProcesses(t *testing.T) {
	for _, role := range []string{"family", "not-leader"} {
		t.Run(role, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDarwinProcessGroupHelper$")
			command.Env = append(os.Environ(), processGroupHelperEnv+"="+role)
			if role == "family" {
				command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("isolated helper: %v\n%s", err, output)
			}
		})
	}
}

func TestDarwinProcessGroupHelper(t *testing.T) {
	switch os.Getenv(processGroupHelperEnv) {
	case "":
		return
	case "family":
		testDarwinProcessGroupFamily(t)
	case "not-leader":
		if peers, err := DarwinGroupHasPeers(context.Background()); err == nil || peers {
			t.Fatalf("inherited group accepted: peers=%v err=%v", peers, err)
		}
	case "child":
		life := os.NewFile(3, "grandchild-lifetime")
		defer life.Close()
		grandchild := processGroupHelperCommand("grandchild")
		grandchild.ExtraFiles = []*os.File{life}
		grandchild.Stdout, grandchild.Stderr = os.Stdout, os.Stderr
		if err := grandchild.Start(); err != nil {
			t.Fatal(err)
		}
		// Intentionally leave the grandchild alive after this direct child exits,
		// as sing-tun's asynchronous shutdown helper does. Its dedicated pipe
		// ends its lifetime even if an ancestor fails or is killed by a timeout.
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatal(err)
		}
	case "grandchild":
		life := os.NewFile(3, "grandchild-lifetime")
		defer life.Close()
		if _, err := io.WriteString(os.Stdout, "grandchild-ready\n"); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, life); err != nil {
			t.Fatal(err)
		}
	case "wait-child":
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unknown process group helper role")
	}
}

func processGroupHelperCommand(role string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestDarwinProcessGroupHelper$")
	command.Env = append(os.Environ(), processGroupHelperEnv+"="+role)
	return command
}

func testDarwinProcessGroupFamily(t *testing.T) {
	check := func(want bool) {
		t.Helper()
		peers, err := DarwinGroupHasPeers(context.Background())
		if err != nil || peers != want {
			t.Fatalf("peers=%v err=%v, want %v", peers, err, want)
		}
	}
	check(false)
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer lifeRead.Close()
	defer lifeWrite.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	child := processGroupHelperCommand("child")
	child.ExtraFiles = []*os.File{lifeRead}
	child.Stdout, child.Stderr = readyWrite, os.Stderr
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	childJoined := false
	t.Cleanup(func() {
		_ = lifeWrite.Close()
		_ = input.Close()
		if !childJoined {
			// No other goroutine reaps this retained direct child.
			_ = child.Process.Kill()
			_ = child.Wait()
		}
		waitDarwinPeersGone(t)
	})
	if err := readyWrite.Close(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(readyRead).ReadString('\n')
	if err != nil || line != "grandchild-ready\n" {
		t.Fatalf("grandchild readiness: %q %v", line, err)
	}
	check(true) // Leader, direct child, and grandchild share this isolated group.
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	childJoined = true
	if err != nil {
		t.Fatal(err)
	}
	check(true) // Reaping the direct child must not hide its surviving child.
	if err := lifeWrite.Close(); err != nil {
		t.Fatal(err)
	}
	waitDarwinPeersGone(t)
	check(false)
}

func waitDarwinPeersGone(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		peers, err := DarwinGroupHasPeers(ctx)
		if err != nil {
			t.Errorf("wait for isolated helper exit: %v", err)
			return
		}
		if !peers {
			return
		}
		select {
		case <-ctx.Done():
			t.Error("isolated descendant did not exit")
			return
		case <-ticker.C:
		}
	}
}
