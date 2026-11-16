package ui

import (
	"net"
	"runtime"
	"strconv"
	"testing"
)

func TestProbeListen_WildcardDetectsIPv4Listener(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Darwin has no TCP occupant table, and binding 0.0.0.0 succeeds while 127.0.0.1 is listening")
	}
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

func TestProbeListen_IPv4ListenerDoesNotBlockIPv6Wildcard(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	probe, err := net.Listen("tcp6", net.JoinHostPort("::", port))
	if err != nil {
		t.Skipf("IPv6 wildcard bind is unavailable here: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	free, err := ProbeListen(net.JoinHostPort("::", port))
	if !free {
		t.Fatalf("IPv6 wildcard blocked by an IPv4 listener: %v", err)
	}
}
