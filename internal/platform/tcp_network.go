package platform

import "net/netip"

// TCPNetwork selects the family of a numeric listen endpoint. In particular,
// probing 0.0.0.0 must not silently bind IPv6 while IPv4 is already occupied.
func TCPNetwork(address string) string {
	endpoint, err := netip.ParseAddrPort(address)
	if err != nil {
		return "tcp"
	}
	if endpoint.Addr().Unmap().Is4() {
		return "tcp4"
	}
	return "tcp6"
}

// TCPDialAddress converts wildcard listen addresses to local destinations while
// retaining concrete addresses. A wildcard is a listener, not a proxy server.
func TCPDialAddress(address string) string {
	endpoint, err := netip.ParseAddrPort(address)
	if err != nil || !endpoint.Addr().Unmap().IsUnspecified() {
		return address
	}
	loopback := netip.IPv6Loopback()
	if endpoint.Addr().Unmap().Is4() {
		loopback = netip.AddrFrom4([4]byte{127, 0, 0, 1})
	}
	return netip.AddrPortFrom(loopback, endpoint.Port()).String()
}
