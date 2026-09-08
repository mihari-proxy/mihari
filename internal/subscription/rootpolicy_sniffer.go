package subscription

import (
	"net/netip"
	"strconv"
	"strings"
)

func snifferSchema() *policySchema {
	ports := listSchema(stringSchema())
	ports.check = func(v policyValue) bool {
		for _, item := range v.items {
			if !validPolicySniffPort(item.text) {
				return false
			}
		}
		return true
	}
	override := boolSchema()
	override.nullable = true
	entry := objectSchema(map[string]*policySchema{"ports": ports, "override-destination": override})
	sniff := objectSchema(map[string]*policySchema{"TLS": entry, "HTTP": entry, "QUIC": entry})
	sniff.normalize = strings.ToUpper
	domain := stringSchema()
	domain.check = func(v policyValue) bool { return validPolicyDomainSelector(v.text) }
	address := stringSchema()
	address.check = func(v policyValue) bool {
		for _, selector := range policyIPSelectors(v.text) {
			if selector.kind == "ipcidr" {
				if _, err := netip.ParsePrefix(selector.value); err != nil {
					return false
				}
			}
		}
		return true
	}
	schema := objectSchema(map[string]*policySchema{
		"enable": boolSchema(), "override-destination": boolSchema(), "force-dns-mapping": boolSchema(), "parse-pure-ip": boolSchema(),
		"sniff": sniff, "sniffing": listSchema(stringSchema()), "port-whitelist": listSchema(stringSchema()),
		"force-domain": listSchema(domain), "skip-domain": listSchema(domain),
		"skip-src-address": listSchema(address), "skip-dst-address": listSchema(address),
	})
	schema.validate = func(v policyValue, field string) error {
		sniff, _ := v.get("sniff")
		if len(sniff.fields) != 0 {
			return nil
		}
		legacy, _ := v.get("sniffing")
		for _, protocol := range legacy.items {
			switch strings.ToUpper(protocol.text) {
			case "TLS", "HTTP", "QUIC":
			default:
				return policyFailure(field + ".sniffing[]")
			}
		}
		ports, _ := v.get("port-whitelist")
		for _, port := range ports.items {
			if !validPolicySniffPort(port.text) {
				return policyFailure(field + ".port-whitelist[]")
			}
		}
		return nil
	}
	return schema
}

// validPolicySniffPort uses the native list-of-ranges grammar, whose empty list
// means every port. It rejects uint64-to-uint16 truncation, without imposing the
// unrelated combined-range parser's wildcard syntax or 28-range count limit.
func validPolicySniffPort(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	parts := strings.Split(text, "-")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if _, err := strconv.ParseUint(strings.Trim(part, "[ ]"), 10, 16); err != nil {
			return false
		}
	}
	return true
}

func policyIPSelectors(text string) []policyDomainSelector {
	lower := strings.ToLower(text)
	for _, kind := range []string{"geoip", "rule-set"} {
		prefix := kind + ":"
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		suffix, _, _ := strings.Cut(text[len(prefix):], ":")
		var selectors []policyDomainSelector
		for value := range strings.SplitSeq(suffix, ",") {
			selectors = append(selectors, policyDomainSelector{kind, value})
		}
		return selectors
	}
	return []policyDomainSelector{{"ipcidr", text}}
}
