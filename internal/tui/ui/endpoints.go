package ui

import (
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/platform"
	"net"
	"net/netip"
	"syscall"
)

// ValidateMixedLAN rejects a listen address that allow-lan=false would ignore.
func ValidateMixedLAN(value string, allowLAN bool) error {
	endpoint, err := netip.ParseAddrPort(value)
	if err != nil || endpoint.Port() == 0 {
		return endpointFailure("mixed endpoint must be an IP address and valid port", value, err)
	}
	if !allowLAN && !endpoint.Addr().IsLoopback() {
		return endpointFailure("Mixed must use a loopback address when Allow LAN is disabled; enable Allow LAN first", value, nil)
	}
	return nil
}

// ValidateManagedEndpoints checks Mixed / Controller / Web the same way Setup does.
func ValidateManagedEndpoints(mixedValue, controllerValue, webValue string) error {
	mixed, err := netip.ParseAddrPort(mixedValue)
	if err != nil || mixed.Port() == 0 {
		return endpointFailure("mixed endpoint must be an IP address and valid port", mixedValue, err)
	}
	controller, err := netip.ParseAddrPort(controllerValue)
	if err != nil || controller.Port() == 0 || !controller.Addr().IsLoopback() {
		return endpointFailure("controller endpoint must use a loopback address and valid port", controllerValue, err)
	}
	web, err := netip.ParseAddrPort(webValue)
	if err != nil || web.Port() == 0 || !web.Addr().IsLoopback() {
		return endpointFailure("web endpoint must use a loopback address and valid port", webValue, err)
	}
	if mixed.Port() == controller.Port() || mixed.Port() == web.Port() || controller.Port() == web.Port() {
		return fmt.Errorf("managed ports must be distinct")
	}
	return nil
}

// ListenFree reports whether addr accepted a short-lived TCP bind.
func ListenFree(addr string) bool {
	// Availability-only callers do not consume diagnostics; the System probe
	// uses ProbeListen directly to retain bind and cleanup failures.
	free, _ := ProbeListen(addr)
	return free
}

// ProbeListen reports availability and preserves any bind or cleanup failure.
func ProbeListen(addr string) (bool, error) {
	// Windows permits overlapping wildcard/specific binds. A successful bind
	// alone cannot establish that the whole wildcard endpoint is available.
	if endpoint, err := netip.ParseAddrPort(addr); err == nil && endpoint.Addr().IsUnspecified() {
		if occupant, ok := platform.LookupTCPOccupant(addr); ok && occupant.PID > 0 {
			return false, fmt.Errorf("listen %s: %w", addr, syscall.EADDRINUSE)
		}
	}
	listener, err := net.Listen(platform.TCPNetwork(addr), addr)
	if err != nil {
		return false, err
	}
	return true, listener.Close()
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

func endpointFailure(summary, value string, cause error) error {
	if cause == nil {
		cause = fmt.Errorf("endpoint %q does not meet the required address and port constraints", value)
	} else {
		cause = fmt.Errorf("parse endpoint %q: %w", value, cause)
	}
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInvalidArgument, Message: summary}, cause)
}
