package config

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// EffectiveProxyAuth returns a detached configuration with explicit defaults.
func (s Settings) EffectiveProxyAuth() protocol.ProxyAuthConfig {
	c := protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow", Users: []protocol.ProxyAuthUser{}, BypassPrefixes: []string{}}
	if s.ProxyAuth != nil {
		c = *s.ProxyAuth
		c.Users = append([]protocol.ProxyAuthUser{}, c.Users...)
		c.BypassPrefixes = append([]string{}, c.BypassPrefixes...)
		if c.Mode == "" {
			c.Mode = "follow"
		}
		if c.BypassMode == "" {
			c.BypassMode = "follow"
		}
	}
	return c
}

// ValidateProxyAuth checks explicit modes, unique credentials and non-loopback CIDRs.
func ValidateProxyAuth(c protocol.ProxyAuthConfig) error {
	if c.Mode != "follow" && c.Mode != "enabled" && c.Mode != "disabled" {
		return dataError("invalid proxy authentication mode")
	}
	if c.BypassMode != "follow" && c.BypassMode != "custom" && c.BypassMode != "required" {
		return dataError("invalid proxy authentication bypass mode")
	}
	if len(c.Users) > 256 || len(c.BypassPrefixes) > 256 {
		return dataError("too many proxy authentication entries")
	}
	seen := map[string]bool{}
	for _, u := range c.Users {
		if !utf8.ValidString(u.Username) || !utf8.ValidString(u.Password) || u.Username == "" || len(u.Username) > 255 || strings.Contains(u.Username, ":") || strings.IndexFunc(u.Username, unicode.IsControl) >= 0 || u.Password == "" || len(u.Password) > 255 || strings.IndexFunc(u.Password, unicode.IsControl) >= 0 {
			return dataError("proxy username and password must be nonempty, at most 255 bytes, without control characters; username cannot contain a colon")
		}
		if seen[u.Username] {
			return dataError("duplicate proxy username")
		}
		seen[u.Username] = true
	}
	v4 := netip.MustParsePrefix("127.0.0.0/8")
	v6 := netip.MustParsePrefix("::1/128")
	seenPrefixes := map[netip.Prefix]bool{}
	for _, raw := range c.BypassPrefixes {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.Addr().Is4In6() {
			return dataError("invalid proxy authentication bypass CIDR")
		}
		prefix = prefix.Masked()
		if prefix.Overlaps(v4) || prefix.Overlaps(v6) {
			return dataError("bypass CIDR overlaps loopback; use the local bypass switch")
		}
		if seenPrefixes[prefix] {
			return dataError("duplicate proxy authentication bypass CIDR")
		}
		seenPrefixes[prefix] = true
	}
	return nil
}
