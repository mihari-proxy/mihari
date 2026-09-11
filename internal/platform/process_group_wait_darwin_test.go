//go:build darwin

package platform

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestDarwinWaitChildExit_PreservesZombieAndIgnoresStop(t *testing.T) {
	child := processGroupHelperCommand("wait-child")
	child.Stderr = os.Stderr
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	var observed, reaped bool
	done := make(chan error, 1)
	started := false
	t.Cleanup(func() {
		if !reaped {
			// DarwinWaitChildExit never reaps, so these retain child ownership.
			_ = child.Process.Signal(syscall.SIGCONT)
			_ = child.Process.Kill()
			_ = input.Close()
			if started && !observed {
				if err := <-done; err != nil {
					t.Errorf("exit observer cleanup: %v", err)
				}
			}
			_ = child.Wait()
		}
	})
	if err = child.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	started = true
	go func() { done <- DarwinWaitChildExit(child.Process.Pid) }()
	select {
	case err := <-done:
		observed = true
		t.Fatalf("stopped child was reported exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = child.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	if err = input.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		observed = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exit observer did not finish")
	}
	// The already-exited child remains observable and waitable by its owner.
	if err = DarwinWaitChildExit(child.Process.Pid); err != nil {
		t.Fatalf("observe unreaped child again: %v", err)
	}
	err = child.Wait()
	reaped = true
	if err != nil {
		t.Fatalf("observer consumed the child's wait status: %v", err)
	}
}

func TestDarwinWaitChildExit_RejectsInvalidPID(t *testing.T) {
	for _, pid := range []int{-1, 0, 1} {
		if err := DarwinWaitChildExit(pid); err == nil {
			t.Fatalf("accepted PID %d", pid)
		}
	}
}
