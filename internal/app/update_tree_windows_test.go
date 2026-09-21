//go:build windows

package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
	"golang.org/x/sys/windows"
)

func TestUpdateRuntimeTreeFixture(t *testing.T) {
	role := os.Getenv("MIHARI_TEST_RUNTIME_TREE")
	if role == "" {
		return
	}
	if role == "leaf" {
		<-time.NewTimer(15 * time.Second).C
		return
	}
	var generation [16]byte
	if _, err := rand.Read(generation[:]); err != nil {
		t.Fatal(err)
	}
	job, err := platform.CreateWindowsRuntimeJob(context.Background(), hex.EncodeToString(generation[:]))
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestUpdateRuntimeTreeFixture$")
	child.Env = append(os.Environ(), "MIHARI_TEST_RUNTIME_TREE=leaf")
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(os.Stdout, "%s %d\n", job.Name(), child.Process.Pid); err != nil {
		t.Fatal(err)
	}
	// Native Job handles belong to this disposable process, including on failure.
	<-time.NewTimer(15 * time.Second).C
	_ = child.Process.Kill()
	_ = child.Wait()
}

func TestUpdateRuntimeTree_NativeMembershipAndWholeTreeExit(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("protected daemon Job requires an elevated test runner")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestUpdateRuntimeTreeFixture$")
	child.Env = append(os.Environ(), "MIHARI_TEST_RUNTIME_TREE=parent")
	pipe, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	line, err := bufio.NewReader(pipe).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var name string
	var leafPID uint32
	if _, err = fmt.Fscan(strings.NewReader(line), &name, &leafPID); err != nil {
		t.Fatal(line, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	process, err := platform.OpenWindowsProcessIdentity(ctx, uint32(child.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := process.Close(); e != nil {
			t.Error(e)
		}
	}()
	leaf, err := windows.OpenProcess(windows.SYNCHRONIZE, false, leafPID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := windows.CloseHandle(leaf); e != nil {
			t.Error(e)
		}
	}()
	tree, err := openUpdateRuntimeTree(ctx, name, process)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := tree.Close(); e != nil {
			t.Error(e)
		}
	}()
	if err = tree.Terminate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = tree.WaitExit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = process.WaitExit(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := windows.WaitForSingleObject(leaf, 3000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("leaf survived: %d %v", state, err)
	}
}
