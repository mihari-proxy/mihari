package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

type darwinGroupTestProcess struct {
	input                 io.WriteCloser
	wait                  func() error
	started, closed, done bool
}

type darwinGroupTestWriteCloser struct {
	calls int
	err   error
}

func (*darwinGroupTestWriteCloser) Write(body []byte) (int, error) { return len(body), nil }
func (c *darwinGroupTestWriteCloser) Close() error {
	c.calls++
	return c.err
}

func (p *darwinGroupTestProcess) closeAndWait() error {
	var err error
	if p.input != nil && !p.closed {
		p.closed = true
		err = errors.Join(err, p.input.Close())
	}
	if p.started && !p.done {
		p.done = true
		err = errors.Join(err, p.wait())
	}
	return err
}

func TestDarwinGroupTestProcess_CloseAndWaitOwnsEachOperationOnce(t *testing.T) {
	closeErr, waitErr := errors.New("close fixture"), errors.New("wait fixture")
	input := &darwinGroupTestWriteCloser{err: closeErr}
	waits := 0
	process := &darwinGroupTestProcess{input: input, started: true, wait: func() error {
		waits++
		return waitErr
	}}
	if err := process.closeAndWait(); !errors.Is(err, closeErr) || !errors.Is(err, waitErr) {
		t.Fatalf("closeAndWait error=%v", err)
	}
	if err := process.closeAndWait(); err != nil {
		t.Fatalf("second closeAndWait repeated an operation: %v", err)
	}
	if input.calls != 1 || waits != 1 {
		t.Fatalf("close calls=%d wait calls=%d", input.calls, waits)
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("MIHARI_SERVICE_GROUP_HELPER") == "1" && len(os.Args) >= 3 && os.Args[1] == "daemon" && os.Args[2] == "--system-service" {
		if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
			os.Exit(2)
		}
		_, err := io.Copy(io.Discard, os.Stdin)
		if err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDarwinLaunchdIdentity_ActualArgumentsAndGroup(t *testing.T) {
	for _, tc := range []struct {
		name           string
		marked, leader bool
	}{
		{"shared", true, true}, {"legacy", false, true}, {"not-leader", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, "daemon", "--system-service")
			if tc.marked {
				cmd.Args = append(cmd.Args, "--launchd-process-group")
			}
			cmd.Env = append(os.Environ(), "MIHARI_SERVICE_GROUP_HELPER=1", "UNTRUSTED_MARKER=--launchd-process-group")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: tc.leader}
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			process := &darwinGroupTestProcess{input: input, wait: cmd.Wait}
			t.Cleanup(func() {
				if err := process.closeAndWait(); err != nil {
					t.Error(err)
				}
			})
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			startErr := cmd.Start()
			if startErr != nil {
				// os/exec.Cmd.Start closes parentIOPipes on every failed start.
				process.closed = true
				t.Fatal(startErr)
			}
			process.started = true
			if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
				t.Fatal("native helper did not become ready")
			}
			tree := darwinProcessTree{}
			id, err := tree.Identify(context.Background(), cmd.Process.Pid)
			if err != nil {
				t.Fatal(err)
			}
			if (id.Group != "") != (tc.marked && tc.leader) {
				t.Fatal("native group authority did not match actual process mode")
			}
			if id.Group != "" {
				if empty, err := tree.Empty(context.Background(), id.Group); err != nil || empty {
					t.Fatal("live group reported absent", err)
				}
			}
			var member *exec.Cmd
			var memberProcess *darwinGroupTestProcess
			if id.Group != "" {
				member = exec.Command(exe, "daemon", "--system-service")
				member.Env = append(os.Environ(), "MIHARI_SERVICE_GROUP_HELPER=1")
				member.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: cmd.Process.Pid}
				memberInput, err := member.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				memberProcess = &darwinGroupTestProcess{input: memberInput, wait: member.Wait}
				t.Cleanup(func() {
					if err := memberProcess.closeAndWait(); err != nil {
						t.Error(err)
					}
				})
				memberOutput, err := member.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				startErr := member.Start()
				if startErr != nil {
					// os/exec.Cmd.Start closes parentIOPipes on every failed start.
					memberProcess.closed = true
					t.Fatal(startErr)
				}
				memberProcess.started = true
				if line, err := bufio.NewReader(memberOutput).ReadString('\n'); err != nil || line != "ready\n" {
					t.Fatal("native group member did not become ready")
				}
			}
			if err := process.closeAndWait(); err != nil {
				t.Fatal(err)
			}
			if id.Group != "" {
				if empty, err := tree.Empty(context.Background(), id.Group); err != nil || empty {
					t.Fatal("leader exit incorrectly proved the shared group empty", err)
				}
				if err := memberProcess.closeAndWait(); err != nil {
					t.Fatal(err)
				}
				if empty, err := tree.Empty(context.Background(), id.Group); err != nil || !empty {
					t.Fatal("exited shared group did not disappear", err)
				}
			}
		})
	}
}
