package platform

import (
	"net"
	"os"
	"runtime"
	"testing"
)

func TestLookupTCPOccupantFindsThisProcessListener(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin occupant lookup is best-effort and may be unavailable without CGO")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	got, ok := LookupTCPOccupant(listener.Addr().String())
	if !ok {
		t.Fatal("expected occupant")
	}
	if got.PID != os.Getpid() {
		t.Fatalf("pid=%d want=%d process=%q", got.PID, os.Getpid(), got.Process)
	}
	if got.Process == "" {
		t.Fatal("empty process name")
	}
}

func TestLookupTCPOccupantUnknownAddress(t *testing.T) {
	if _, ok := LookupTCPOccupant("127.0.0.1:1"); ok {
		t.Fatal("did not expect occupant on unused port 1")
	}
}
