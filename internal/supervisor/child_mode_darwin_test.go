//go:build darwin

package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/platform"
)

const sharedGroupHelperEnv = "MIHARI_SHARED_GROUP_HELPER"
const sharedGroupDirEnv = "MIHARI_SHARED_GROUP_FIXTURE"

func init() {
	role := os.Getenv(sharedGroupHelperEnv)
	if role == "" {
		return
	}
	dir := os.Getenv(sharedGroupDirEnv)
	if err := runSharedGroupHelper(role, dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestDarwinSharedChild_DescendantsAndSignalOwnership(t *testing.T) {
	for _, mode := range []string{"daemon", "observation-error"} {
		t.Run(mode, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			command := exec.Command(executable)
			command.Env = sharedGroupHelperEnvironment(mode, t.TempDir())
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			// Retain the unreaped group leader until the final group cleanup;
			// the group number cannot be reused underneath this fixture.
			exited := make(chan error, 1)
			go func() { exited <- platform.DarwinWaitChildExit(command.Process.Pid) }()
			observed := false
			t.Cleanup(func() {
				killErr := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
				if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
					t.Error(killErr)
				}
				if !observed {
					if err := <-exited; err != nil {
						t.Error(err)
					}
				}
				if err := command.Wait(); err != nil {
					t.Errorf("shared group helper: %v: %s", err, output.String())
				}
			})
			select {
			case err := <-exited:
				observed = true
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("shared group helper did not exit")
			}
		})
	}
}

func sharedGroupHelperEnvironment(role, dir string) []string {
	env := append([]string{}, os.Environ()...)
	return append(env, sharedGroupHelperEnv+"="+role, sharedGroupDirEnv+"="+dir)
}

func runSharedGroupHelper(role, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	switch role {
	case "grandchild":
		if err := os.WriteFile(filepath.Join(dir, "grandchild-ready"), []byte("ready"), 0600); err != nil {
			return err
		}
		return sharedGroupWaitFile(ctx, filepath.Join(dir, "release-grandchild"))
	case "core":
		signal.Ignore(syscall.SIGTERM)
		grandchild := exec.Command(executable)
		grandchild.Env = sharedGroupHelperEnvironment("grandchild", dir)
		if err := grandchild.Start(); err != nil {
			return err
		}
		// The fixture intentionally reproduces an asynchronously spawned
		// descendant that survives its parent; the outer owner cleans the group.
		raw, err := json.Marshal([]int{os.Getpid(), syscall.Getpgrp(), grandchild.Process.Pid})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "core-ready"), raw, 0600); err != nil {
			return err
		}
		return sharedGroupWaitFile(ctx, filepath.Join(dir, "unused-core-release"))
	case "daemon", "observation-error":
	default:
		return errors.New("unknown shared group fixture")
	}
	starter := CommandStarter{ShareProcessGroup: true, CommandFactory: func(context.Context) (core.CoreCommand, func() error, error) {
		return core.CoreCommand{Binary: executable, Home: dir, Env: sharedGroupHelperEnvironment("core", dir)}, func() error { return nil }, nil
	}}
	child, err := starter.Start()
	if err != nil {
		return err
	}
	for _, name := range []string{"core-ready", "grandchild-ready"} {
		if err := sharedGroupWaitFile(ctx, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "core-ready"))
	if err != nil {
		return err
	}
	var pids []int
	if err := json.Unmarshal(raw, &pids); err != nil {
		return err
	}
	if len(pids) != 3 || pids[0] != child.PID() || pids[1] != os.Getpid() {
		return errors.New("core did not inherit daemon process group")
	}
	owned := child.(*sharedDarwinChild)
	observationFailure := errors.New("injected exit observation failure")
	if role == "observation-error" {
		owned.waitExit = func(int) error { return observationFailure }
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	if err := child.Terminate(); err != nil {
		return err
	}
	if err := child.Kill(); err != nil {
		return err
	}
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	err = owned.WaitDescendants(short)
	stop()
	if err == nil {
		return errors.New("leader exit released live descendant")
	}
	if err := os.WriteFile(filepath.Join(dir, "release-grandchild"), []byte("exit"), 0600); err != nil {
		return err
	}
	if role == "observation-error" {
		if !errors.Is(owned.WaitDescendants(ctx), observationFailure) {
			return errors.New("exit observation failure was lost")
		}
		// Observe cleanup directly: an intentionally failed owner proof must
		// remain a failure even once all of its descendants happen to exit.
		for {
			peers, err := platform.DarwinGroupHasPeers(ctx)
			if err != nil {
				return err
			}
			if !peers {
				break
			}
			if err := (realWaiter{}).Wait(ctx, 10*time.Millisecond); err != nil {
				return err
			}
		}
	} else if err := owned.WaitDescendants(ctx); err != nil {
		return err
	}
	return child.Kill() // An already reaped core cannot receive another signal.
}

func sharedGroupWaitFile(ctx context.Context, name string) error {
	for {
		if _, err := os.Stat(name); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := (realWaiter{}).Wait(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
}
