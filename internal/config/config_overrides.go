package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// EffectiveOverrides returns a detached snapshot of the editable values.
func (s Settings) EffectiveOverrides() protocol.ConfigOverrides {
	if s.Overrides == nil {
		return protocol.ConfigOverrides{}
	}
	return s.Overrides.Clone()
}

// SetOverrides canonicalizes the empty state and detaches caller-owned slices.
func (s *Settings) SetOverrides(input protocol.ConfigOverrides) {
	if len(input.DNS) == 0 && len(input.Fragments) == 0 {
		s.Overrides = nil
		return
	}
	cloned := input.Clone()
	s.Overrides = &cloned
}

// ValidateOverrides checks editable identities and typed DNS form operations.
func ValidateOverrides(input *protocol.ConfigOverrides) error {
	if input == nil {
		return nil
	}
	ids := make(map[string]bool, len(input.Fragments))
	for _, f := range input.Fragments {
		if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.Name) == "" {
			return dataError("fragment ID and name are required")
		}
		if ids[f.ID] {
			return dataError("duplicate fragment ID: " + f.ID)
		}
		ids[f.ID] = true
	}
	for field, op := range input.DNS {
		if !validDNSField(field, op) {
			return dataError("invalid DNS override for " + field + " (" + op.Operation + ")")
		}
		if field == "default-nameserver" && (op.Operation == "set" || op.Operation == "append") {
			if op.Operation == "set" && len(op.Servers) == 0 {
				return dataError("dns.default-nameserver cannot be an empty list")
			}
			for _, server := range op.Servers {
				if !IsBootstrapAddress(server) {
					return dataError(fmt.Sprintf("dns.default-nameserver %q needs an IP address or system resolver", server))
				}
			}
		}
	}
	return nil
}

func validDNSField(field string, op protocol.DNSFieldOverride) bool {
	boolean := field == "enable" || field == "ipv6" || field == "respect-rules"
	mode := field == "enhanced-mode"
	list := field == "nameserver" || field == "default-nameserver" || field == "proxy-server-nameserver" || field == "fallback"
	if !boolean && !mode && !list {
		return false
	}
	if op.Operation == "delete" {
		return op.Bool == nil && op.Mode == "" && len(op.Servers) == 0
	}
	if op.Operation != "set" && !(list && (op.Operation == "append" || op.Operation == "remove")) {
		return false
	}
	switch {
	case boolean:
		return op.Bool != nil && op.Mode == "" && len(op.Servers) == 0
	case mode:
		return op.Bool == nil && len(op.Servers) == 0 && (op.Mode == "redir-host" || op.Mode == "fake-ip" || op.Mode == "normal")
	default:
		if op.Bool != nil || op.Mode != "" {
			return false
		}
		for _, server := range op.Servers {
			if strings.TrimSpace(server) == "" {
				return false
			}
		}
		return true
	}
}

// IsBootstrapAddress checks the bootstrap address requirement for an effective
// default-nameserver. The core validates the selected resolver protocol.
func IsBootstrapAddress(server string) bool {
	if server == "system" {
		return true
	}
	if _, err := netip.ParseAddr(server); err == nil {
		return true
	}
	if !strings.Contains(server, "://") {
		server = "udp://" + server
	}
	endpoint, err := url.Parse(server)
	if err != nil {
		return false
	}
	switch endpoint.Scheme {
	case "system":
		// mihomo selects its system resolver by scheme, ignoring the host.
		return true
	case "dhcp":
		// mihomo leaves the DHCP identifier verbatim as its resolver address.
		// Unlike udp/tcp/tls it does not add a default port before checking it.
		_, address, found := strings.Cut(server, "://")
		if !found {
			return false
		}
		if address == "system" {
			return true
		}
		if host, _, err := net.SplitHostPort(address); err == nil && net.ParseIP(host) != nil {
			return true
		}
		u, err := url.Parse(address)
		if err != nil {
			// The core's bootstrap IP check skips unparseable DHCP identifiers.
			return true
		}
		return net.ParseIP(u.Hostname()) != nil
	}
	_, err = netip.ParseAddr(endpoint.Hostname())
	return err == nil
}
