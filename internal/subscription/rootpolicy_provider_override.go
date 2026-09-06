package subscription

import "math"

// The first twelve fields are the exact assignments in pinned override.Apply.
// Their values are checked again in each selected protocol's field schema.
var providerSimpleOverrideNames = [...]string{
	"tfo", "mptcp", "udp", "udp-over-tcp", "up", "down", "dialer-proxy",
	"skip-cert-verify", "name-cert-verify", "interface-name", "routing-mark", "ip-version",
}

func providerOverrideSchema() *policySchema {
	fields := make(map[string]*policySchema)
	for _, name := range []string{"tfo", "mptcp", "udp", "udp-over-tcp", "skip-cert-verify"} {
		field := boolSchema()
		field.nullable = true
		fields[name] = field
	}
	for _, name := range []string{"up", "down", "dialer-proxy", "name-cert-verify", "interface-name", "ip-version", "additional-prefix", "additional-suffix"} {
		field := stringSchema()
		field.nullable = true
		fields[name] = field
	}
	mark := integerSchema(math.MinInt64, math.MaxInt64)
	mark.nullable = true
	fields["routing-mark"] = mark
	// These expressions only write name. Preserve .NET syntax as string data;
	// the same trusted candidate core checks its regex/replacement semantics.
	rename := objectSchema(map[string]*policySchema{"pattern": stringSchema(), "target": stringSchema()})
	rename.required = []string{"pattern", "target"}
	fields["proxy-name"] = listSchema(rename)
	fields["override-expr"] = &policySchema{reject: true}
	schema := objectSchema(fields)
	schema.nullable = true
	return schema
}

func providerPayloadProxySchema(override policyValue, dialer string) *policySchema {
	schema := rootProxySchema()
	for _, variant := range schema.variants {
		overlays := make(map[string]policyValue)
		if dialer != "" {
			if _, declared := variant.fields["dialer-proxy"]; declared {
				overlays["dialer-proxy"] = policyValue{kind: policyString, text: dialer}
			}
		}
		for _, name := range providerSimpleOverrideNames {
			value, exists := override.get(name)
			if _, declared := variant.fields[name]; declared && exists && value.kind != policyNull {
				overlays[name] = value
			}
		}
		variant.memberOverlays = overlays
	}
	return schema
}
