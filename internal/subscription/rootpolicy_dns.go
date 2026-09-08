package subscription

import (
	"context"
	"math"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func dnsSchema() *policySchema {
	fields := make(map[string]*policySchema)
	for _, name := range []string{"enable", "prefer-h3", "ipv6", "use-hosts", "use-system-hosts", "respect-rules", "fallback-lazy-query", "direct-nameserver-follow-policy"} {
		fields[name] = boolSchema()
	}
	for _, name := range []string{"nameserver", "fallback", "default-nameserver", "proxy-server-nameserver", "direct-nameserver"} {
		fields[name] = listSchema(nameserverSchema())
	}
	fields["ipv6-timeout"] = unsignedSchema(math.MaxInt64 / uint64(time.Millisecond))
	fields["listen"] = stringSchema()
	fields["listen"].check = func(v policyValue) bool {
		if v.text == "" {
			return true
		}
		host, port, err := net.SplitHostPort(v.text)
		if err != nil {
			return false
		}
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return false
		}
		// The fixed inbound preResolve consumer special-cases exact localhost
		// to loopback without consulting DNS or the system hosts file.
		if host == "localhost" {
			return true
		}
		addr, err := netip.ParseAddr(host)
		return err == nil && addr.IsLoopback() && addr.Zone() == ""
	}
	// Linux SetsockoptInt carries the signed or unsigned spelling of one 32-bit
	// SO_MARK bit pattern; other Unix targets treat this option as a no-op.
	fields["listen-routing-mark"] = integerSchema(math.MinInt32, math.MaxUint32)
	fields["enhanced-mode"] = enumSchema("normal", "fake-ip", "redir-host")
	fields["fake-ip-filter-mode"] = enumSchema("blacklist", "whitelist", "rule")
	for _, name := range []string{"fake-ip-range", "fake-ip-range6", "cache-algorithm"} {
		fields[name] = stringSchema()
	}
	fields["fake-ip-filter"] = listSchema(stringSchema())
	fields["fake-ip-ttl"] = integerSchema(math.MinInt64, math.MaxInt64)
	fields["cache-max-size"] = integerSchema(math.MinInt64, math.MaxInt64)
	fields["nameserver-policy"] = dnsOrderedPolicySchema()
	fields["proxy-server-nameserver-policy"] = dnsOrderedPolicySchema()
	fields["fallback-filter"] = objectSchema(map[string]*policySchema{
		"geoip": boolSchema(), "geoip-code": stringSchema(),
		"ipcidr": listSchema(stringSchema()), "domain": listSchema(stringSchema()), "geosite": listSchema(stringSchema()),
	})
	schema := objectSchema(fields)
	schema.validate = validatePolicyDNS
	schema.transform = transformPolicyDNSRules
	return schema
}

func transformPolicyDNSRules(ctx context.Context, v policyValue, field string) (policyValue, error) {
	if xhttpText(v, "enhanced-mode") != "fake-ip" || xhttpText(v, "fake-ip-filter-mode") != "rule" {
		return v, nil
	}
	filters, present := v.get("fake-ip-filter")
	if !present {
		// The three native default Windows-connectivity domain strings have no
		// rule action and are invalid when this explicitly selected mode parses
		// them as rules. An explicit empty list remains a valid empty ruleset.
		return policyValue{}, policyFailure(field + ".fake-ip-filter")
	}
	for i, item := range filters.items {
		rule, err := parsePolicyRule(ctx, item.text, true, field+".fake-ip-filter[]")
		if err != nil {
			return policyValue{}, err
		}
		rule.target = strings.ToLower(rule.target)
		if rule.target != "fake-ip" && rule.target != "real-ip" {
			return policyValue{}, policyFailure(field + ".fake-ip-filter[]")
		}
		switch rule.kind {
		case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-REGEX", "DOMAIN-WILDCARD", "GEOSITE", "RULE-SET", "MATCH":
		default:
			return policyValue{}, policyFailure(field + ".fake-ip-filter[]")
		}
		filters.items[i] = policyValue{kind: policyString, text: rule.encode(), rule: rule}
	}
	v.set("fake-ip-filter", filters)
	return v, nil
}

func dnsOrderedPolicySchema() *policySchema {
	schema := &policySchema{kind: policyObject, dynamic: stringListUnion(nameserverSchema()), nullable: true}
	schema.transform = func(ctx context.Context, v policyValue, field string) (policyValue, error) {
		trie := newPolicyDomainTrie()
		for _, member := range v.fields {
			if err := ctx.Err(); err != nil {
				return policyValue{}, err
			}
			selectors := policyDomainSelectors(member.name, true)
			for _, selector := range selectors {
				if selector.kind != "domain" {
					// Native resolver flushes a contiguous ordinary-domain trie at
					// each matcher. Preserve this ordering boundary in fresh YAML.
					trie = newPolicyDomainTrie()
					continue
				}
				if !trie.add(selector.value, 0) {
					return policyValue{}, policyFailure(field + ".[entry]")
				}
			}
		}
		return v, nil
	}
	return schema
}

type policyDomainSelector struct{ kind, value string }

// policyDomainSelectors reproduces the two fixed parseDomain / ordered-policy
// grammars. The category or provider name is data for later resource closure.
func policyDomainSelectors(text string, ordered bool) []policyDomainSelector {
	lower := strings.ToLower(text)
	for _, kind := range []string{"geosite", "rule-set"} {
		prefix := kind + ":"
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		suffix := text[len(prefix):]
		if !ordered || strings.Contains(text, ",") {
			suffix, _, _ = strings.Cut(suffix, ":")
		}
		var selectors []policyDomainSelector
		for value := range strings.SplitSeq(suffix, ",") {
			selectors = append(selectors, policyDomainSelector{kind, value})
		}
		return selectors
	}
	if !ordered {
		return []policyDomainSelector{{"domain", text}}
	}
	var selectors []policyDomainSelector
	for value := range strings.SplitSeq(text, ",") {
		selectors = append(selectors, policyDomainSelector{"domain", value})
	}
	return selectors
}

func validPolicyDomainSelector(text string) bool {
	for _, selector := range policyDomainSelectors(text, false) {
		if selector.kind == "domain" {
			if _, ok := policyDomainParts(selector.value); !ok {
				return false
			}
		}
	}
	return true
}

func validatePolicyDNS(v policyValue, field string) error {
	fail := func(name string) error { return policyFailure(field + "." + name) }
	main, present := v.get("nameserver")
	enabled, _ := v.get("enable")
	if enabled.boolean && present && len(main.items) == 0 {
		return fail("nameserver")
	}
	proxy, _ := v.get("proxy-server-nameserver")
	policy, _ := v.get("proxy-server-nameserver-policy")
	respectRules, _ := v.get("respect-rules")
	if (respectRules.boolean || len(policy.fields) > 0) && len(proxy.items) == 0 {
		return fail("proxy-server-nameserver")
	}
	if defaults, ok := v.get("default-nameserver"); ok {
		if len(defaults.items) == 0 {
			return fail("default-nameserver")
		}
		for _, server := range defaults.items {
			parsed, err := parsePolicyNameserver(server.text)
			if err != nil {
				return fail("default-nameserver[]")
			}
			if parsed.scheme == "system" {
				continue
			}
			if net.ParseIP(parsed.host) == nil {
				return fail("default-nameserver[]")
			}
		}
	}
	fake := xhttpText(v, "enhanced-mode") == "fake-ip"
	validPools := 0
	for _, name := range []string{"fake-ip-range", "fake-ip-range6"} {
		prefix, ok := v.get(name)
		if !ok && name == "fake-ip-range" {
			prefix.text = "198.18.0.1/16"
		}
		if prefix.text == "" {
			continue
		}
		parsed, err := netip.ParsePrefix(prefix.text)
		if err != nil || (name == "fake-ip-range" && !parsed.Addr().Is4()) || (name == "fake-ip-range6" && !parsed.Addr().Is6()) {
			return fail(name)
		}
		// Native pool starts at network+4 and requires it strictly before the
		// final address. Exactly three host bits is the minimum valid pool.
		if fake && parsed.Addr().BitLen()-parsed.Bits() < 3 {
			return fail(name)
		}
		validPools++
	}
	if fake && validPools == 0 {
		return fail("fake-ip-range")
	}
	if fake {
		if ttl, _ := v.get("fake-ip-ttl"); ttl.integer > math.MaxUint32 {
			return fail("fake-ip-ttl")
		}
		filters, _ := v.get("fake-ip-filter")
		if xhttpText(v, "fake-ip-filter-mode") != "rule" {
			for _, filter := range filters.items {
				if !validPolicyDomainSelector(filter.text) {
					return fail("fake-ip-filter[]")
				}
			}
		}
	}
	if size, _ := v.get("cache-max-size"); xhttpText(v, "cache-algorithm") == "arc" && size.integer > math.MaxInt64/2 {
		return fail("cache-max-size")
	}
	fallback, _ := v.get("fallback")
	if len(fallback.items) != 0 {
		filter, _ := v.get("fallback-filter")
		prefixes, _ := filter.get("ipcidr")
		for _, prefix := range prefixes.items {
			if _, err := netip.ParsePrefix(prefix.text); err != nil {
				return fail("fallback-filter.ipcidr[]")
			}
		}
		domains, _ := filter.get("domain")
		for _, domain := range domains.items {
			if _, ok := policyDomainParts(domain.text); !ok {
				return fail("fallback-filter.domain[]")
			}
		}
	}
	return nil
}

func nameserverSchema() *policySchema {
	schema := stringSchema()
	schema.check = func(v policyValue) bool { _, err := parsePolicyNameserver(v.text); return err == nil }
	return schema
}

// policyNameserver records the network identity used by the resource graph.
// URI path/userinfo/query/fragment interpretation is fixed by the pinned core;
// no field is interpreted as an operating-system filename.
type policyNameserver struct {
	scheme, host, proxy string
	port                uint16
}

func parsePolicyNameserver(value string) (policyNameserver, error) {
	fail := func() (policyNameserver, error) { return policyNameserver{}, policyFailure("nameserver") }
	if value == "system" {
		value = "system://"
	} else if address, err := netip.ParseAddr(value); err == nil {
		if address.Is4() {
			value = "udp://" + value
		} else {
			value = "udp://[" + value + "]"
		}
	} else if !strings.Contains(value, "://") {
		value = "udp://" + value
	}
	u, err := url.Parse(value)
	if err != nil {
		return fail()
	}
	parsed := policyNameserver{scheme: u.Scheme, host: u.Host}
	for fragment := range strings.SplitSeq(u.Fragment, "&") {
		name, argument, parameter := strings.Cut(fragment, "=")
		if !parameter {
			// Names are exact network lookup data, including an encoded NUL
			// matching an already validated proxy name. They never form paths.
			parsed.proxy = name
			continue
		}
		switch name {
		case "h3", "skip-cert-verify", "name-cert-verify", "disable-reuse", "disable-ipv4", "disable-ipv6", "ecs-override":
			// The native boolean predicates only recognize exact "true". Other
			// values are preserved as known inactive string data, without folding.
		case "ecs":
			if argument != "" {
				if _, err := netip.ParsePrefix(argument); err != nil {
					if _, err := netip.ParseAddr(argument); err != nil {
						return fail()
					}
				}
			}
		default:
			if !strings.HasPrefix(name, "disable-qtype-") {
				return fail()
			}
			code, err := strconv.ParseUint(strings.TrimPrefix(name, "disable-qtype-"), 10, 16)
			if err != nil || !registeredDNSQuestionType(uint16(code)) {
				return fail()
			}
		}
	}
	defaultPort := ""
	switch u.Scheme {
	case "udp", "tcp":
		defaultPort = "53"
	case "tls", "quic":
		defaultPort = "853"
	case "http":
		defaultPort = "80"
	case "https":
		defaultPort = "443"
	case "system":
		return parsed, nil
	case "dhcp":
		if value != "dhcp://system" {
			return fail()
		}
		parsed.scheme = "system"
		parsed.host = ""
		return parsed, nil
	case "ts", "tailscale":
		if u.Host == "" {
			return fail()
		}
		return parsed, nil
	case "rcode":
		switch u.Host {
		case "success", "format_error", "server_failure", "name_error", "not_implemented", "refused":
			return parsed, nil
		default:
			return fail()
		}
	default:
		return fail()
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		host, port, err = net.SplitHostPort(u.Host + ":" + defaultPort)
	}
	if err != nil || host == "" || strings.ContainsAny(host, " /\\\t\r\n\x00") {
		return fail()
	}
	numeric, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return fail()
	}
	parsed.host, parsed.port = host, uint16(numeric)
	return parsed, nil
}

func registeredDNSQuestionType(value uint16) bool {
	for _, r := range [][2]uint16{{1, 10}, {12, 21}, {23, 33}, {35, 37}, {39, 39}, {41, 53}, {55, 65}, {99, 102}, {104, 109}, {128, 128}, {249, 250}, {255, 258}, {260, 260}, {32768, 32769}} {
		if value >= r[0] && value <= r[1] {
			return true
		}
	}
	return false
}
