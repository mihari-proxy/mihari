//go:build windows

package transport

import (
	"context"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"golang.org/x/sys/windows"
	"net"
	"os"
	"testing"
	"time"
)

// TestUpdateOwner_RejectsUnprovableCreationTime validates the fail-closed PID-reuse guard on a real pipe.
func TestUpdateOwner_RejectsUnprovableCreationTime(t *testing.T) {
	endpoint := fmt.Sprintf(`\\.\pipe\mihari-update-owner-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	listener, err := Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := DialContext(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case err := <-acceptErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer conn.Close()
	native := conn.(*updateConn)
	acceptStarted := native.acceptStarted
	native.acceptStarted = 0
	owner, err := OpenUpdateOwner(UpdateConnContext(ctx, conn))
	if owner != nil {
		_ = owner.Close()
		t.Fatal("unproved owner accepted")
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Message != protocol.UpdateFreshConnectionMessage {
		t.Fatalf("unexpected native proof failure: %v", err)
	}
	native.acceptStarted = acceptStarted
	owner, err = OpenUpdateOwner(UpdateConnContext(ctx, conn))
	if windows.GetCurrentProcessToken().IsElevated() {
		if err != nil {
			t.Fatal(err)
		}
		if err = owner.Close(); err != nil {
			t.Fatal(err)
		}
	} else if err == nil || owner != nil {
		if owner != nil {
			_ = owner.Close()
		}
		t.Fatal("non-elevated caller authorized")
	}
}
