package config

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProxyAuthSettings is the persisted instance authentication intent, independent of wire DTOs.
type ProxyAuthSettings struct {
	Mode           string                `yaml:"mode"`
	Users          []ProxyAuthCredential `yaml:"users,omitempty"`
	BypassMode     string                `yaml:"bypass-mode,omitempty"`
	LocalBypass    bool                  `yaml:"local-bypass,omitempty"`
	BypassPrefixes []string              `yaml:"bypass-prefixes,omitempty"`
}

// ProxyAuthCredential is one saved username/password override.
type ProxyAuthCredential struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// EffectiveProxyAuth explicitly converts saved intent into a detached wire configuration.
func (s Settings) EffectiveProxyAuth() protocol.ProxyAuthConfig {
	c := protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow", Users: []protocol.ProxyAuthUser{}, BypassPrefixes: []string{}}
	if saved := s.ProxyAuth; saved != nil {
		c.Mode, c.BypassMode, c.LocalBypass = saved.Mode, saved.BypassMode, saved.LocalBypass
		for _, user := range saved.Users {
			c.Users = append(c.Users, protocol.ProxyAuthUser{Username: user.Username, Password: user.Password})
		}
		c.BypassPrefixes = append(c.BypassPrefixes, saved.BypassPrefixes...)
		if c.Mode == "" {
			c.Mode = "follow"
		}
		if c.BypassMode == "" {
			c.BypassMode = "follow"
		}
	}
	return c
}

// SetProxyAuth converts a wire candidate to persistent intent and omits default-equivalent overrides.
func (s *Settings) SetProxyAuth(c protocol.ProxyAuthConfig) {
	if c.Mode == "" {
		c.Mode = "follow"
	}
	if c.BypassMode == "" {
		c.BypassMode = "follow"
	}
	if c.Mode == "follow" && c.BypassMode == "follow" && !c.LocalBypass && len(c.Users) == 0 && len(c.BypassPrefixes) == 0 {
		s.ProxyAuth = nil
		return
	}
	saved := &ProxyAuthSettings{Mode: c.Mode, BypassMode: c.BypassMode, LocalBypass: c.LocalBypass, BypassPrefixes: append([]string{}, c.BypassPrefixes...)}
	for _, user := range c.Users {
		saved.Users = append(saved.Users, ProxyAuthCredential{Username: user.Username, Password: user.Password})
	}
	s.ProxyAuth = saved
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
		if prefix.Overlaps(v4) || prefix.Overlaps(v6) || prefix.Overlaps(netip.MustParsePrefix("::ffff:0:0/96")) {
			return dataError("bypass CIDR overlaps loopback; use the local bypass switch")
		}
		if seenPrefixes[prefix] {
			return dataError("duplicate proxy authentication bypass CIDR")
		}
		seenPrefixes[prefix] = true
	}
	return nil
}
