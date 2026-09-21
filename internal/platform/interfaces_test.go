package platform

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestNetworkInterfaces_LinkStateOrderingAndCancellation(t *testing.T) {
	list := func() ([]net.Interface, error) {
		return []net.Interface{{Name: "z VPN", Flags: net.FlagUp | net.FlagRunning | net.FlagPointToPoint}, {Name: "a down", Flags: net.FlagUp}}, nil
	}
	addrs := func(net.Interface) ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.5"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	got, err := enumerateNetworkInterfaces(t.Context(), list, addrs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a down" || got[0].Availability != "disconnected" || got[1].Kind != "virtual" || got[1].Availability != "available" || got[1].Addresses[0] != "192.0.2.5/24" {
		t.Fatalf("got=%+v", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := enumerateNetworkInterfaces(ctx, func() ([]net.Interface, error) { t.Fatal("enumerated after cancel"); return nil, nil }, addrs); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
func TestNetworkInterfaces_EnumerationFailureIsNotEmptySuccess(t *testing.T) {
	failure := errors.New("enumeration failed")
	_, err := enumerateNetworkInterfaces(t.Context(), func() ([]net.Interface, error) { return nil, failure }, nil)
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
