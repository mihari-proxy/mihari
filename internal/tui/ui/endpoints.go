package ui

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// ValidateManagedEndpoints checks Mixed / Controller / Web the same way Setup does.
func ValidateManagedEndpoints(mixedValue, controllerValue, webValue string) error {
	mixed, err := netip.ParseAddrPort(mixedValue)
	if err != nil || mixed.Port() == 0 {
		return fmt.Errorf("mixed endpoint must be an IP address and valid port")
	}
	controller, err := netip.ParseAddrPort(controllerValue)
	if err != nil || controller.Port() == 0 || !controller.Addr().IsLoopback() {
		return fmt.Errorf("controller endpoint must use a loopback address and valid port")
	}
	web, err := netip.ParseAddrPort(webValue)
	if err != nil || web.Port() == 0 || !web.Addr().IsLoopback() {
		return fmt.Errorf("web endpoint must use a loopback address and valid port")
	}
	if mixed.Port() == controller.Port() || mixed.Port() == web.Port() || controller.Port() == web.Port() {
		return fmt.Errorf("managed ports must be distinct")
	}
	return nil
}

// ListenFree reports whether addr accepted a short-lived TCP bind.
func ListenFree(addr string) bool {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// AddrInUse reports whether a listen error means the address is already bound.
func AddrInUse(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.EADDRINUSE || errno == 10048
}

// FindAvailablePorts rewrites occupied endpoints to the next free port.
// occupied[i] true means that slot should be moved. probe reports bindability.
func FindAvailablePorts(current [3]string, occupied [3]bool, probe func(string) bool) [3]string {
	if probe == nil {
		probe = ListenFree
	}
	result := current
	used := make(map[uint16]bool)
	for index, addr := range current {
		parsed, err := netip.ParseAddrPort(addr)
		if err != nil {
			continue
		}
		if !occupied[index] {
			used[parsed.Port()] = true
			continue
		}
		host := parsed.Addr()
		for offset := 1; offset <= 1024; offset++ {
			candidate := uint16(parsed.Port()) + uint16(offset)
			if used[candidate] {
				continue
			}
			candidateAddr := netip.AddrPortFrom(host, candidate).String()
			if probe(candidateAddr) {
				result[index] = candidateAddr
				used[candidate] = true
				break
			}
		}
	}
	return result
}
