package config

import (
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
