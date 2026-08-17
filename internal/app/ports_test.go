package app

import (
	"errors"
	"net"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestProbeManagedPortsReportsOccupantDetails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	address := listener.Addr().String()
	settings := config.Settings{MixedAddr: address, ControllerAddr: "127.0.0.1:18091", WebAddr: "127.0.0.1:18092"}
	err = probeManagedPorts(settings, func(got string) (platform.TCPOccupant, bool) {
		if got != address {
			return platform.TCPOccupant{}, false
		}
		return platform.TCPOccupant{PID: 4321, Process: "clash.exe"}, true
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidState || apiError.Message != "managed port is unavailable" {
		t.Fatalf("err=%v", err)
	}
	if apiError.Details["setting"] != "mixed-addr" || apiError.Details["address"] != address {
		t.Fatalf("details=%v", apiError.Details)
	}
	if apiError.Details["pid"] != 4321 || apiError.Details["process"] != "clash.exe" {
		t.Fatalf("details=%v", apiError.Details)
	}
}

func TestProbeManagedPortsOmitsOccupantWhenLookupMisses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	settings := config.Settings{
		MixedAddr: listener.Addr().String(), ControllerAddr: "127.0.0.1:18091", WebAddr: "127.0.0.1:18092",
	}
	err = probeManagedPorts(settings, func(string) (platform.TCPOccupant, bool) {
		return platform.TCPOccupant{}, false
	})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("err=%v", err)
	}
	if _, ok := apiError.Details["pid"]; ok {
		t.Fatalf("details=%v", apiError.Details)
	}
	if _, ok := apiError.Details["process"]; ok {
		t.Fatalf("details=%v", apiError.Details)
	}
}
