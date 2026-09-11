//go:build unix_security && (linux || darwin)

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/securitytest"
)

func TestSecurityPeerOwner(t *testing.T) {
	layout, uids, gids := securitytest.Parent(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	lease, err := platform.AcquireDaemonLease(ctx, layout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	}()
	listener, err := ListenOwned(ctx, layout, lease)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	received := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- err
			return
		}
		var one [1]byte
		_, err = io.ReadFull(conn, one[:])
		received <- errors.Join(err, conn.Close())
	}()
	locator, err := layout.Locator(0)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := DialVerified(ctx, locator)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := conn.Write([]byte{1})
	if err := errors.Join(writeErr, conn.Close(), <-received); err != nil {
		t.Fatal(err)
	}

	// UID A creates and owns the listening peer. Root subsequently secures the
	// exact directory/socket inodes, isolating peer rejection from namespace refusal.
	attack := filepath.Join(os.Getenv("MIHARI_SECURITY_ROOT"), "peer-attack")
	if err := os.Mkdir(attack, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(attack, int(uids[0]), int(gids[0])); err != nil {
		t.Fatal(err)
	}
	input := securitytest.Input{Defaults: platform.SystemLayoutDefaults(), Own: attack}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	defaultsRead, defaultsWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = defaultsRead.Close(); _ = defaultsWrite.Close() }()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestSecurityPeerChild$")
	cmd.Env = []string{"MIHARI_UNIX_SECURITY_CHILD=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw)
	cmd.ExtraFiles = []*os.File{defaultsRead}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uids[0], Gid: gids[0], Groups: []uint32{}}}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		if !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	if err := defaultsRead.Close(); err != nil {
		t.Fatal(err)
	}
	envelope := struct {
		Schema   string                  `json:"schema"`
		Defaults platform.LayoutDefaults `json:"defaults"`
	}{"mihari.unix-security-defaults/v1", input.Defaults}
	if err := errors.Join(json.NewEncoder(defaultsWrite).Encode(envelope), defaultsWrite.Close()); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(io.LimitReader(stdout, 65536))
	var ready struct {
		EUID  uint32
		Ready bool
	}
	if err := decoder.Decode(&ready); err != nil || ready.EUID != uids[0] || !ready.Ready {
		t.Fatal("actual UID peer not ready")
	}
	socket := filepath.Join(attack, "socket")
	before, err := os.Lstat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(socket, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(attack, 0, 0); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(socket)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("peer fixture socket replaced")
	}
	bad := platform.ControlLocator{Mode: platform.PrivateMode, BaseDir: attack, Endpoint: socket, Credential: filepath.Join(attack, "token"), ExpectedOwner: 0}
	conn, err = DialVerified(ctx, bad)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("foreign UID peer accepted")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("wrong peer refusal: %v", err)
	}
	var result struct {
		EUID     uint32
		Bytes    int
		Accepted bool
	}
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.EUID != uids[0] || result.Bytes != 0 || !result.Accepted {
		t.Fatal("peer rejection did not precede all authentication IO")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	joined = true
}

func TestSecurityPeerChild(t *testing.T) {
	input := securitytest.ReadInput(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(input.Own, "socket"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		EUID  uint32
		Ready bool
	}{uint32(os.Geteuid()), true}); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	n, err := conn.Read(buf[:])
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF before authentication: %v", err)
	}
	if err := errors.Join(conn.Close(), listener.Close()); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		EUID     uint32
		Bytes    int
		Accepted bool
	}{uint32(os.Geteuid()), n, true}); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
