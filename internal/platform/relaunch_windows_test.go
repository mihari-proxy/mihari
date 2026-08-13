//go:build windows

package platform

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeReplacementProcess struct {
	waitEntered  chan struct{}
	waitUnblock  chan struct{}
	waitErr      error
	releaseErr   error
	waitCalls    int
	releaseCalls int
}

func (process *fakeReplacementProcess) Wait() (*os.ProcessState, error) {
	process.waitCalls++
	if process.waitEntered != nil {
		close(process.waitEntered)
	}
	if process.waitUnblock != nil {
		<-process.waitUnblock
	}
	return nil, process.waitErr
}

func (process *fakeReplacementProcess) Release() error {
	process.releaseCalls++
	return process.releaseErr
}

func TestRelaunchRequiresBinaryAndArgs(t *testing.T) {
	prev := startProcess
	t.Cleanup(func() { startProcess = prev })
	startProcess = func(string, []string, *os.ProcAttr) (replacementProcess, error) {
		t.Fatal("startProcess should not run for invalid input")
		return nil, nil
	}

	if err := Relaunch("", []string{"mihari"}, nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("empty binary err=%v", err)
	}
	if err := Relaunch("C:\\mihari.exe", nil, nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("empty args err=%v", err)
	}
}

func TestRelaunchStartsWithConsoleInheritedFiles(t *testing.T) {
	prev := startProcess
	t.Cleanup(func() { startProcess = prev })

	var (
		gotName string
		gotArgs []string
		gotAttr *os.ProcAttr
	)
	process := &fakeReplacementProcess{}
	startProcess = func(name string, argv []string, attr *os.ProcAttr) (replacementProcess, error) {
		gotName = name
		gotArgs = append([]string(nil), argv...)
		gotAttr = attr
		return process, nil
	}

	env := []string{"MIHARI_DATA=C:\\data", "PATH=C:\\Windows"}
	err := Relaunch(`C:\Program Files\mihari\mihari.exe`, []string{`C:\Program Files\mihari\mihari.exe`, "daemon"}, env)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if process.waitCalls != 1 || process.releaseCalls != 0 {
		t.Fatalf("waitCalls=%d releaseCalls=%d", process.waitCalls, process.releaseCalls)
	}
	if gotName != `C:\Program Files\mihari\mihari.exe` {
		t.Fatalf("name=%q", gotName)
	}
	if len(gotArgs) != 2 || gotArgs[1] != "daemon" {
		t.Fatalf("args=%v", gotArgs)
	}
	if gotAttr == nil {
		t.Fatal("missing ProcAttr")
	}
	if len(gotAttr.Env) != 2 || gotAttr.Env[0] != env[0] {
		t.Fatalf("env=%v", gotAttr.Env)
	}
	if len(gotAttr.Files) != 3 || gotAttr.Files[0] != os.Stdin || gotAttr.Files[1] != os.Stdout || gotAttr.Files[2] != os.Stderr {
		t.Fatalf("files=%v want stdin/stdout/stderr inheritance", gotAttr.Files)
	}
}

func TestRelaunchWaitsForReplacementBeforeReturning(t *testing.T) {
	prev := startProcess
	t.Cleanup(func() { startProcess = prev })

	entered := make(chan struct{})
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	unblockWait := func() { unblockOnce.Do(func() { close(unblock) }) }
	t.Cleanup(unblockWait)
	process := &fakeReplacementProcess{waitEntered: entered, waitUnblock: unblock}
	startProcess = func(string, []string, *os.ProcAttr) (replacementProcess, error) {
		return process, nil
	}

	result := make(chan error, 1)
	go func() {
		result <- Relaunch(`C:\mihari.exe`, []string{`C:\mihari.exe`}, nil)
	}()

	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("Relaunch returned without waiting for replacement: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Relaunch did not call replacement Wait")
	}
	select {
	case err := <-result:
		t.Fatalf("Relaunch returned before replacement exited: %v", err)
	default:
	}
	unblockWait()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Relaunch did not return after replacement exited")
	}
}

func TestRelaunchReleasesProcessAfterWaitFailure(t *testing.T) {
	prev := startProcess
	t.Cleanup(func() { startProcess = prev })

	waitErr := errors.New("injected wait failure")
	process := &fakeReplacementProcess{waitErr: waitErr}
	startProcess = func(string, []string, *os.ProcAttr) (replacementProcess, error) {
		return process, nil
	}

	err := Relaunch(`C:\mihari.exe`, []string{`C:\mihari.exe`}, nil)
	if !errors.Is(err, waitErr) || !strings.Contains(err.Error(), "wait for updated Mihari") {
		t.Fatalf("err=%v", err)
	}
	if process.releaseCalls != 1 {
		t.Fatalf("releaseCalls=%d want=1", process.releaseCalls)
	}
}

func TestRelaunchPreservesWaitAndReleaseErrors(t *testing.T) {
	prev := startProcess
	t.Cleanup(func() { startProcess = prev })

	waitErr := errors.New("injected wait failure")
	releaseErr := errors.New("injected release failure")
	process := &fakeReplacementProcess{waitErr: waitErr, releaseErr: releaseErr}
	startProcess = func(string, []string, *os.ProcAttr) (replacementProcess, error) {
		return process, nil
	}

	err := Relaunch(`C:\mihari.exe`, []string{`C:\mihari.exe`}, nil)
	if !errors.Is(err, waitErr) || !errors.Is(err, releaseErr) {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "wait for updated Mihari") || !strings.Contains(err.Error(), "release updated Mihari after wait failure") {
		t.Fatalf("err=%v", err)
	}
	if process.waitCalls != 1 || process.releaseCalls != 1 {
		t.Fatalf("waitCalls=%d releaseCalls=%d", process.waitCalls, process.releaseCalls)
	}
}
