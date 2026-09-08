package service

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

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
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			joined := false
			t.Cleanup(func() {
				if !joined {
					if err := input.Close(); err != nil {
						t.Error(err)
					}
					if err := cmd.Wait(); err != nil {
						t.Error(err)
					}
				}
			})
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
			var memberInput io.WriteCloser
			memberJoined := true
			if id.Group != "" {
				member = exec.Command(exe, "daemon", "--system-service")
				member.Env = append(os.Environ(), "MIHARI_SERVICE_GROUP_HELPER=1")
				member.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: cmd.Process.Pid}
				memberInput, err = member.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				memberOutput, err := member.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := member.Start(); err != nil {
					t.Fatal(err)
				}
				memberJoined = false
				t.Cleanup(func() {
					if !memberJoined {
						if err := memberInput.Close(); err != nil {
							t.Error(err)
						}
						if err := member.Wait(); err != nil {
							t.Error(err)
						}
					}
				})
				if line, err := bufio.NewReader(memberOutput).ReadString('\n'); err != nil || line != "ready\n" {
					t.Fatal("native group member did not become ready")
				}
			}
			if err := input.Close(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			joined = true
			if id.Group != "" {
				if empty, err := tree.Empty(context.Background(), id.Group); err != nil || empty {
					t.Fatal("leader exit incorrectly proved the shared group empty", err)
				}
				if err := memberInput.Close(); err != nil {
					t.Fatal(err)
				}
				if err := member.Wait(); err != nil {
					t.Fatal(err)
				}
				memberJoined = true
				if empty, err := tree.Empty(context.Background(), id.Group); err != nil || !empty {
					t.Fatal("exited shared group did not disappear", err)
				}
			}
		})
	}
}
