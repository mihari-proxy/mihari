//go:build unix

package platform

import (
	"errors"
	"strings"
	"testing"
)

func TestRelaunchRequiresBinaryAndArgs(t *testing.T) {
	prev := execProcess
	t.Cleanup(func() { execProcess = prev })
	execProcess = func(string, []string, []string) error {
		t.Fatal("execProcess should not run for invalid input")
		return nil
	}

	if err := Relaunch("", []string{"mihari"}, nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("empty binary err=%v", err)
	}
	if err := Relaunch("/usr/local/bin/mihari", nil, nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("empty args err=%v", err)
	}
}

func TestRelaunchExecPassesBinaryArgsAndEnv(t *testing.T) {
	prev := execProcess
	t.Cleanup(func() { execProcess = prev })

	var (
		gotBinary string
		gotArgs   []string
		gotEnv    []string
	)
	execProcess = func(argv0 string, argv []string, envv []string) error {
		gotBinary = argv0
		gotArgs = append([]string(nil), argv...)
		gotEnv = append([]string(nil), envv...)
		return errors.New("injected exec failure")
	}

	env := []string{"MIHARI_DATA=/tmp/mihari", "PATH=/usr/bin"}
	err := Relaunch("/opt/mihari/mihari", []string{"/opt/mihari/mihari", "daemon"}, env)
	if err == nil || !strings.Contains(err.Error(), "exec updated Mihari") {
		t.Fatalf("err=%v", err)
	}
	if gotBinary != "/opt/mihari/mihari" {
		t.Fatalf("binary=%q", gotBinary)
	}
	if len(gotArgs) != 2 || gotArgs[1] != "daemon" {
		t.Fatalf("args=%v", gotArgs)
	}
	if len(gotEnv) != 2 || gotEnv[0] != env[0] {
		t.Fatalf("env=%v", gotEnv)
	}
}
