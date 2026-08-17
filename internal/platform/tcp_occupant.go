package platform

import (
	"net"
	"strconv"
)

// TCPOccupant is the process holding a local TCP listen address.
type TCPOccupant struct {
	PID     int
	Process string
}

func parseTCPAddress(address string) (net.IP, int, bool) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, 0, false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, 0, false
	}
	return ip, port, true
}

// listenIPMatches reports whether a table local address occupies query.
// Unspecified listeners (0.0.0.0 / ::) occupy every address in their family;
// IPv6 unspecified also occupies IPv4 when the stack is dual-bind.
func listenIPMatches(local, query net.IP) bool {
	if local == nil || query == nil {
		return false
	}
	if local.Equal(query) {
		return true
	}
	if !local.IsUnspecified() {
		return false
	}
	if local.To4() != nil {
		return query.To4() != nil
	}
	return true
}
