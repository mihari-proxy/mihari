package subscription

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

func applyDNSForm(document Document, origins *fieldOrigin, form map[string]protocol.DNSFieldOverride) error {
	if len(form) == 0 {
		return nil
	}
	dns := egressMapping(document["dns"])

	for _, key := range slices.Sorted(maps.Keys(form)) {
		change := form[key]
		if (change.Operation == "append" || change.Operation == "remove") && len(change.Servers) == 0 {
			continue
		}
		if dns == nil && document["dns"] != nil {
			return errors.New("dns must be a mapping to apply the DNS form")
		}
		op := replaceField
		var value any
		switch change.Operation {
		case "delete":
			op = removeFieldOrItems
		case "set", "append", "remove":
			switch {
			case change.Bool != nil:
				value = *change.Bool
			case change.Mode != "":
				value = change.Mode
			default:
				list := make([]any, len(change.Servers))
				for i, server := range change.Servers {
					list[i] = server
				}
				value = list
			}
			if change.Operation == "append" {
				op = appendItems
			}
			if change.Operation == "remove" {
				op = removeFieldOrItems
			}
		default:
			return fmt.Errorf("dns.%s: unknown operation %q", key, change.Operation)
		}
		if dns == nil {
			if op == removeFieldOrItems {
				continue
			}
			dns = map[string]any{}
			document["dns"] = dns
		}
		origin := origins.field("dns")
		if err := applyFieldOperation(dns, origin.field(key), key, op, value, "DNS form"); err != nil {
			return fmt.Errorf("dns.%s: %w", key, err)
		}
		origin.source = "DNS form"
	}
	return nil
}

// The core supplies default nameserver and default-nameserver lists for omitted
// fields. Explicit empty lists replace those defaults.
func validateEffectiveDNS(document Document) error {
	raw, exists := document["dns"]
	if !exists || raw == nil {
		return nil
	}
	dns := egressMapping(raw)
	if dns == nil {
		return errors.New("dns must be a mapping")
	}
	// Typed YAML boolean decoding also accepts legacy unquoted yes/no forms
	// used in native configuration.
	var flags struct {
		Enable, IPv6 bool
		RespectRules bool `yaml:"respect-rules"`
	}
	flagValues := map[string]any{"enable": dns["enable"], "ipv6": dns["ipv6"], "respect-rules": dns["respect-rules"]}
	var flagNode yaml.Node
	if err := flagNode.Encode(flagValues); err != nil {
		return fmt.Errorf("encode DNS flags: %w", err)
	}
	if err := flagNode.Decode(&flags); err != nil {
		return fmt.Errorf("invalid DNS flag: %w", err)
	}
	if value := dns["enhanced-mode"]; value != nil {
		mode, _ := value.(string)
		switch strings.ToLower(mode) {
		case "normal", "fake-ip", "redir-host":
		default:
			return fmt.Errorf("invalid dns.enhanced-mode: %v", value)
		}
	}
	for _, field := range []string{"nameserver", "default-nameserver", "proxy-server-nameserver", "fallback"} {
		value, present := dns[field]
		if !present {
			continue
		}
		servers, ok := value.([]any)
		if !ok {
			return fmt.Errorf("dns.%s must be a list", field)
		}
		for _, server := range servers {
			s, ok := server.(string)
			if !ok || s == "" {
				return fmt.Errorf("dns.%s requires nonempty server strings", field)
			}
			if field == "default-nameserver" && !config.IsBootstrapAddress(s) {
				return fmt.Errorf("dns.default-nameserver %q needs an IP address or system resolver", s)
			}
		}
		if len(servers) == 0 && (field == "default-nameserver" || field == "nameserver" && flags.Enable) {
			return fmt.Errorf("dns.%s cannot be an empty list", field)
		}
	}
	if flags.RespectRules || len(egressMapping(dns["proxy-server-nameserver-policy"])) > 0 {
		servers, _ := dns["proxy-server-nameserver"].([]any)
		if len(servers) == 0 {
			return errors.New("dns.proxy-server-nameserver is required by respect-rules or proxy-server-nameserver-policy")
		}
	}
	return nil
}
