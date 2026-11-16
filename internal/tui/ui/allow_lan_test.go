package ui

import (
	"net"
	"strconv"
	"testing"
)

func TestProbeListen_WildcardDetectsIPv4Listener(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	address := net.JoinHostPort("0.0.0.0", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	free, err := ProbeListen(address)
	if free {
		t.Fatalf("IPv4 wildcard incorrectly available while IPv4 loopback is listening: %v", err)
	}
}
