package app

import (
	"errors"
	"net"
	"net/netip"
	"reflect"
	"runtime"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestProbeManagedPorts_WildcardDetectsSpecificListener(t *testing.T) {
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
	settings := config.Defaults()
	settings.AllowLAN = true
	settings.MixedAddr = netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(listener.Addr().(*net.TCPAddr).Port)).String()
	err = probeManagedPorts(settings, nil)
	var conflict *ManagedPortConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("wildcard probe missed listener: %v", err)
	}
}

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

type portProbeListener struct {
	net.Listener
	closed *int
}

func (l portProbeListener) Close() error { *l.closed++; return nil }

func TestProbeManagedPorts_InjectedListenerReceivesAndClosesEachEndpoint(t *testing.T) {
	settings := config.Defaults()
	listeners := map[string]net.Listener{}
	for _, address := range []*string{&settings.MixedAddr, &settings.ControllerAddr, &settings.WebAddr} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := listener.Close(); err != nil {
				t.Error(err)
			}
		})
		*address = listener.Addr().String()
		listeners[*address] = listener
	}
	var addresses []string
	closed := 0
	err := probeManagedPortsWithListener(settings, nil, func(network, address string) (net.Listener, error) {
		listener, ok := listeners[address]
		if network != "tcp4" || !ok {
			return nil, errors.New("unexpected endpoint")
		}
		addresses = append(addresses, address)
		return portProbeListener{Listener: listener, closed: &closed}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(addresses, []string{settings.MixedAddr, settings.ControllerAddr, settings.WebAddr}) || closed != 3 {
		t.Fatalf("probe did not visit and close all selected endpoints: %v closes=%d", addresses, closed)
	}
	for address := range listeners {
		listener, err := net.Listen("tcp", address)
		if err == nil {
			_ = listener.Close()
			t.Fatal("injected probe released reserved endpoint")
		}
	}
}
