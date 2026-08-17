package app

import (
	"net"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func probeManagedPorts(settings config.Settings, lookup func(string) (platform.TCPOccupant, bool)) error {
	if lookup == nil {
		lookup = platform.LookupTCPOccupant
	}
	for _, endpoint := range []struct{ setting, address string }{
		{"mixed-addr", settings.MixedAddr},
		{"controller-addr", settings.ControllerAddr},
		{"web-addr", settings.WebAddr},
	} {
		listener, err := net.Listen("tcp", endpoint.address)
		if err != nil {
			details := map[string]any{"setting": endpoint.setting, "address": endpoint.address}
			if occupant, ok := lookup(endpoint.address); ok && occupant.PID > 0 {
				details["pid"] = occupant.PID
				details["process"] = filepath.Base(occupant.Process)
			}
			return protocol.APIError{
				Code: protocol.CodeInvalidState, Message: "managed port is unavailable",
				Details: details,
			}
		}
		_ = listener.Close()
	}
	return nil
}
