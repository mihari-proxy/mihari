package subscription

import (
	"context"
	"math"
	"net/netip"
	"strconv"
	"strings"
)

// policyRule is a closed discriminated rule AST. Payload storage follows the
// registered kind: scalar match data, parsed prefix, ordered ranges or names.
// Resource references remain names, never filesystem paths or command inputs.
type policyRule struct {
	kind, text, target           string
	prefix                       netip.Prefix
	ranges                       []policyRuleRange
	names                        []string
	source, noResolve, hasTarget bool
	children                     []*policyRule
}

type policyRuleRange struct{ low, high uint64 }

func policyRuleListSchema() *policySchema {
	element := policyRuleSchema(true)
	element.nullable = true // Native []string decoder skips null rule elements.
	list := listSchema(element)
	list.nullable = true
	list.transform = func(ctx context.Context, v policyValue, field string) (policyValue, error) {
		items := make([]policyValue, 0, len(v.items))
		for _, item := range v.items {
			if item.kind != policyNull {
				items = append(items, item)
			}
		}
		v.items = items
		return v, nil
	}
	return list
}

func subRulesSchema() *policySchema {
	schema := &policySchema{kind: policyObject, dynamic: policyRuleListSchema(), nullable: true}
	schema.validate = func(v policyValue, field string) error {
		for _, member := range v.fields {
			if member.name == "" {
				return policyFailure(field + ".[entry]")
			}
		}
		return nil
	}
	return schema
}

func policyRuleSchema(target bool) *policySchema {
	schema := stringSchema()
	schema.transform = func(ctx context.Context, v policyValue, field string) (policyValue, error) {
		rule, err := parsePolicyRule(ctx, v.text, target, field)
		if err != nil {
			return policyValue{}, err
		}
		return policyValue{kind: policyString, text: rule.encode(), rule: rule}, nil
	}
	return schema
}

func parsePolicyRule(ctx context.Context, text string, target bool, field string) (*policyRule, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parts := strings.Split(text, ",")
	for i := range parts {
		parts[i] = strings.Trim(parts[i], " ")
	}
	r, params := policyRuleHeader(strings.Join(parts, ","), target)
	if (target && r.target == "") || (r.kind != "MATCH" && r.text == "") {
		return nil, policyFailure(field)
	}
	if isPolicyLogic(r.kind) {
		if err := parsePolicyLogic(ctx, r, field); err != nil {
			return nil, err
		}
	} else if err := validatePolicyRuleLeaf(r, params, field); err != nil {
		return nil, err
	}
	return r, nil
}

// policyRuleHeader receives comma-normalized text. Composite children can use
// slices of the same normalized input; they do not repeatedly split/copy their
// entire nested subtree while parsing a deep chain.
func policyRuleHeader(text string, target bool) (*policyRule, []string) {
	kind, payload, comma := strings.Cut(text, ",")
	r := &policyRule{kind: strings.ToUpper(strings.Trim(kind, " ")), hasTarget: target}
	var params []string
	if comma {
		switch r.kind {
		case "MATCH":
			r.target, _, _ = strings.Cut(payload, ",")
			r.target = strings.Trim(r.target, " ")
		case "DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX", "NOT", "OR", "AND", "SUB-RULE":
			if target {
				last := strings.LastIndexByte(payload, ',')
				if last < 0 {
					r.target, payload = strings.Trim(payload, " "), ""
				} else {
					r.target, payload = strings.Trim(payload[last+1:], " "), payload[:last]
				}
			}
			r.text = strings.Trim(payload, " ")
		default:
			parts := strings.Split(payload, ",")
			for i := range parts {
				parts[i] = strings.Trim(parts[i], " ")
			}
			r.text = parts[0]
			start := 1
			if target && len(parts) > 1 {
				r.target = parts[1]
				start = 2
			}
			if len(parts) > start {
				params = parts[start:]
			}
		}
	}
	return r, params
}

func validatePolicyRuleLeaf(r *policyRule, params []string, field string) error {
	fail := func() error { return policyFailure(field) }
	parameters := false
	switch r.kind {
	case "MATCH":
	case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD", "DOMAIN-WILDCARD":
		r.text = strings.ToLower(r.text)
	case "DOMAIN-REGEX", "PROCESS-NAME-REGEX", "PROCESS-PATH-REGEX":
		// Native regexp2 is pure matching data with no replacement or ability
		// to write a configuration field. The same trusted core validates its
		// complete .NET syntax; substituting RE2 would reject valid patterns.
	case "PROCESS-NAME", "PROCESS-PATH", "PROCESS-NAME-WILDCARD", "PROCESS-PATH-WILDCARD":
	case "GEOSITE", "IP-ASN", "SRC-IP-ASN", "RULE-SET":
		parameters = r.kind == "IP-ASN" || r.kind == "RULE-SET"
	case "GEOIP", "SRC-GEOIP":
		r.text = strings.ToLower(r.text)
		parameters = r.kind == "GEOIP"
	case "IP-CIDR", "IP-CIDR6", "SRC-IP-CIDR", "IP-SUFFIX", "SRC-IP-SUFFIX":
		prefix, err := netip.ParsePrefix(r.text)
		if err != nil {
			return fail()
		}
		r.prefix = prefix
		r.text = ""
		parameters = !strings.HasPrefix(r.kind, "SRC-")
	case "SRC-PORT", "DST-PORT", "IN-PORT", "DSCP", "UID":
		maximum := uint64(math.MaxUint16)
		if r.kind == "UID" {
			maximum = math.MaxUint32
		}
		if r.kind == "DSCP" {
			maximum = 63
		}
		ranges, ok := parsePolicyRuleRanges(r.text, maximum)
		if !ok || (r.kind != "DSCP" && len(ranges) == 0) {
			return fail()
		}
		r.ranges, r.text = ranges, ""
	case "NETWORK":
		r.text = strings.ToUpper(r.text)
		if r.text != "TCP" && r.text != "UDP" {
			return fail()
		}
	case "IN-TYPE", "IN-USER", "IN-NAME", "REMATCH-NAME":
		for name := range strings.SplitSeq(r.text, "/") {
			name = strings.TrimSpace(name)
			if name == "" {
				return fail()
			}
			if r.kind == "IN-TYPE" {
				name = strings.ToUpper(name)
				switch name {
				case "SOCKS", "HTTP", "HTTPS", "SOCKS4", "SOCKS5", "SHADOWSOCKS", "SNELL", "VMESS", "VLESS", "REDIR", "TPROXY", "TROJAN", "TUNNEL", "TUN", "TUIC", "HYSTERIA2", "ANYTLS", "MIERU", "SUDOKU", "TRUSTTUNNEL", "SHADOWQUIC", "INNER":
				default:
					return fail()
				}
			}
			r.names = append(r.names, name)
		}
		r.text = ""
	default:
		return fail()
	}
	if parameters {
		for _, param := range params {
			if param == "src" {
				r.source = true
			}
			if param == "no-resolve" {
				r.noResolve = true
			}
		}
		if r.source {
			r.noResolve = false
		} // src already implies no-resolve.
	}
	return nil
}

func parsePolicyRuleRanges(text string, maximum uint64) ([]policyRuleRange, bool) {
	text = strings.TrimSpace(text)
	if text == "" || text == "*" {
		return nil, true
	}
	parts := strings.Split(strings.ReplaceAll(text, ",", "/"), "/")
	if len(parts) > 28 {
		return nil, false
	}
	var ranges []policyRuleRange
	for _, raw := range parts {
		if raw == "" {
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			ranges = append(ranges, policyRuleRange{})
			continue
		}
		endpoints := strings.Split(raw, "-")
		if len(endpoints) > 2 {
			return nil, false
		}
		low, err := strconv.ParseUint(strings.Trim(endpoints[0], "[ ]"), 10, 64)
		if err != nil || low > maximum {
			return nil, false
		}
		high := low
		if len(endpoints) == 2 {
			high, err = strconv.ParseUint(strings.Trim(endpoints[1], "[ ]"), 10, 64)
			if err != nil || high > maximum {
				return nil, false
			}
		}
		if low > high {
			low, high = high, low
		}
		ranges = append(ranges, policyRuleRange{low, high})
	}
	return ranges, true
}

func (r *policyRule) encodeLeaf() string {
	if r.kind == "MATCH" {
		if r.hasTarget {
			return "MATCH," + r.target
		}
		return "MATCH"
	}
	payload := r.text
	if r.prefix.IsValid() {
		payload = r.prefix.String()
	}
	if r.ranges != nil || r.kind == "DSCP" {
		if len(r.ranges) == 0 {
			payload = "*"
		} else {
			parts := make([]string, 0, len(r.ranges))
			for _, span := range r.ranges {
				part := strconv.FormatUint(span.low, 10)
				if span.low != span.high {
					part += "-" + strconv.FormatUint(span.high, 10)
				}
				parts = append(parts, part)
			}
			payload = strings.Join(parts, "/")
		}
	}
	if r.names != nil {
		payload = strings.Join(r.names, "/")
	}
	result := r.kind + "," + payload
	if r.hasTarget {
		result += "," + r.target
	}
	if r.source {
		result += ",src"
	} else if r.noResolve {
		result += ",no-resolve"
	}
	return result
}
