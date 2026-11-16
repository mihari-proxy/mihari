package app

import (
	"net"
	"net/netip"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func probeManagedPorts(settings config.Settings, lookup func(string) (platform.TCPOccupant, bool)) error {
	return probeManagedPortsWithListener(settings, lookup, nil)
}

// probeManagedPortsWithListener tests configured endpoints and marks only confirmed address conflicts.
func probeManagedPortsWithListener(settings config.Settings, lookup func(string) (platform.TCPOccupant, bool), listen func(string, string) (net.Listener, error)) error {
	if listen == nil {
		listen = net.Listen
	}
	wildcardLookup := lookup
	if wildcardLookup == nil {
		wildcardLookup = platform.LookupWildcardBindOccupant
	}
	if lookup == nil {
		lookup = platform.LookupTCPOccupant
	}
	for _, endpoint := range []struct{ setting, address string }{
		{"mixed-addr", settings.MixedAddr},
		{"controller-addr", settings.ControllerAddr},
		{"web-addr", settings.WebAddr},
	} {
		if address, err := netip.ParseAddrPort(endpoint.address); err == nil && address.Addr().Unmap().IsUnspecified() {
			normalized := netip.AddrPortFrom(address.Addr().Unmap(), address.Port()).String()
			if occupant, ok := wildcardLookup(normalized); ok && occupant.PID > 0 {
				return &ManagedPortConflict{failure: protocol.APIError{Code: protocol.CodeInvalidState, Message: "managed port is unavailable",
					Details: map[string]any{"setting": endpoint.setting, "address": endpoint.address, "pid": occupant.PID, "process": filepath.Base(occupant.Process)}}}
			}
		}
		listener, err := listen(platform.TCPNetwork(endpoint.address), endpoint.address)
		if err != nil {
			details := map[string]any{"setting": endpoint.setting, "address": endpoint.address}
			if occupant, ok := lookup(endpoint.address); ok && occupant.PID > 0 {
				details["pid"] = occupant.PID
				details["process"] = filepath.Base(occupant.Process)
			}
			failure := protocol.APIError{
				Code: protocol.CodeInvalidState, Message: "managed port is unavailable",
				Details: details,
			}
			if platform.PortInUse(err) {
				return &ManagedPortConflict{failure: failure}
			}
			return failure
		}
		_ = listener.Close()
	}
	return nil
}
