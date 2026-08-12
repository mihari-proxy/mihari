//go:build windows

package platform

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRelaunchRequiresBinaryAndArgs(t *testing.T) {
	prev := startProcess
	t.Cleanup(func() { startProcess = prev })
	startProcess = func(string, []string, *os.ProcAttr) (*os.Process, error) {
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
	startProcess = func(name string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
		gotName = name
		gotArgs = append([]string(nil), argv...)
		gotAttr = attr
		// Process with Pid 0 cannot Release successfully on Windows; return a
		// synthetic error path is not needed because we only inspect attr construction.
		// Returning an error after capturing args proves no real process spawn.
		return nil, errors.New("injected start failure")
	}

	env := []string{"MIHARI_DATA=C:\\data", "PATH=C:\\Windows"}
	err := Relaunch(`C:\Program Files\mihari\mihari.exe`, []string{`C:\Program Files\mihari\mihari.exe`, "daemon"}, env)
	if err == nil || !strings.Contains(err.Error(), "start updated Mihari") {
		t.Fatalf("err=%v", err)
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
