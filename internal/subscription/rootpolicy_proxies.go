package subscription

import (
	"context"
	"math"
	"strings"
)

func proxyBaseFields() map[string]*policySchema {
	preference := stringSchema()
	preference.transform = func(_ context.Context, v policyValue, _ string) (policyValue, error) {
		v.text = strings.ToLower(v.text)
		switch v.text {
		case "dual", "ipv4", "ipv6", "ipv4-prefer", "ipv6-prefer":
		default:
			v.text = "dual"
		}
		return v, nil
	}
	return map[string]*policySchema{
		// Names are exact lookup data, including empty and NUL strings. They
		// never supply local paths; server addresses have a separate schema.
		"name": stringSchema(), "type": stringSchema(),
		"tfo": boolSchema(), "mptcp": boolSchema(),
		"interface-name": stringSchema(), "routing-mark": unsignedSchema(math.MaxUint32),
		"ip-version":   preference,
		"dialer-proxy": stringSchema(),
	}
}

func proxyObject(fields map[string]*policySchema) *policySchema {
	fields["smux"] = muxSchema()
	s := objectSchema(fields)
	s.required = []string{"name", "type"}
	aliases := make(map[string]string, len(fields))
	for name := range fields {
		aliases[strings.ToLower(name)] = name
	}
	s.normalize = func(name string) string {
		if _, ok := fields[name]; ok {
			return name
		}
		if canonical, ok := aliases[strings.ToLower(strings.ReplaceAll(name, "_", "-"))]; ok {
			return canonical
		}
		return name
	}
	return s
}

func rootProxySchema() *policySchema {
	variants := make(map[string]*policySchema)
	for _, kind := range []string{"direct", "dns", "reject"} {
		variants[kind] = proxyObject(proxyBaseFields())
	}
	rematch := proxyBaseFields()
	target := stringSchema()
	target.nullable = true
	rematch["target-rematch-name"], rematch["target-sub-rule"] = target, target
	s := proxyObject(rematch)
	s.check = func(v policyValue) bool {
		name, hasName := v.get("target-rematch-name")
		rule, hasRule := v.get("target-sub-rule")
		return (hasName && name.kind != policyNull) || (hasRule && rule.kind != policyNull)
	}
	variants["rematch"] = s
	variants["ssh"] = sshProxySchema()
	variants["hysteria2"] = hysteria2ProxySchema()
	variants["snell"] = snellProxySchema()
	variants["trojan"] = trojanProxySchema()
	variants["tuic"] = tuicProxySchema()
	variants["vmess"] = vmessProxySchema()
	variants["ss"] = ssProxySchema()
	variants["masque"] = masqueProxySchema()
	variants["openvpn"] = openVPNProxySchema()
	variants["wireguard"] = wireGuardProxySchema()
	variants["vless"] = vlessProxySchema()
	variants["mieru"] = mieruProxySchema()
	variants["anytls"] = anyTLSProxySchema()
	variants["trusttunnel"] = trustTunnelProxySchema()
	variants["sudoku"] = sudokuProxySchema()
	variants["ssr"] = ssrProxySchema()
	variants["shadowquic"] = shadowQUICProxySchema()
	variants["hysteria"] = hysteriaProxySchema()
	for _, kind := range []string{"socks5", "http", "gost-relay"} {
		fields := proxyBaseFields()
		fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
		fields["username"], fields["password"], fields["tls"] = stringSchema(), stringSchema(), boolSchema()
		addClientTLSFields(fields)
		if kind != "socks5" {
			fields["sni"] = stringSchema()
		}
		if kind != "http" {
			fields["udp"] = boolSchema()
		}
		if kind == "http" {
			fields["headers"] = policyHeadersSchema(false)
		}
		if kind == "gost-relay" {
			fields["forward"], fields["mux"] = boolSchema(), boolSchema()
			fields["client-fingerprint"] = stringSchema()
		}
		option := proxyObject(fields)
		option.required = append(option.required, "server", "port")
		option.validate = func(v policyValue, field string) error {
			if !validClientKeyPair(v) {
				return policyFailure(field + ".certificate")
			}
			// SOCKS5 serializes active credential lengths as uint8 without a
			// native guard. Gost checks the same byte domain before writing.
			// HTTP Basic uses a separate dynamically sized base64 encoding.
			username, _ := v.get("username")
			if kind == "gost-relay" || kind == "socks5" && username.text != "" {
				for _, credential := range []string{"username", "password"} {
					value, _ := v.get(credential)
					if len(value.text) > 255 {
						return policyFailure(field + "." + credential)
					}
				}
			}
			return nil
		}
		variants[kind] = option
	}
	return &policySchema{kind: policyObject, discriminator: "type", variants: variants}
}
