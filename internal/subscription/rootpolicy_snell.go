package subscription

import (
	"math"
	"strings"
)

func snellProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"], fields["psk"] = policyNameSchema(), integerSchema(1, 65535), stringSchema()
	fields["version"], fields["reuse"], fields["udp"] = integerSchema(0, 5), boolSchema(), boolSchema()
	fields["client-fingerprint"], fields["obfs-opts"] = stringSchema(), snellObfuscationSchema()
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "psk")
	schema.validate = func(v policyValue, field string) error {
		version, _ := v.get("version")
		udp, _ := v.get("udp")
		if udp.boolean && version.integer < 3 {
			return policyFailure(field + ".udp")
		}
		return nil
	}
	return schema
}

// The obfs map has a closed union of native mode-specific fields. Inactive
// fields remain typed and credential-bearing fields always receive validation.
func snellObfuscationSchema() *policySchema {
	fields := map[string]*policySchema{
		"mode": {kind: policyString, choices: []string{"", "http", "tls", "shadow-tls", "restls", "jls"}},
		"host": stringSchema(), "password": stringSchema(), "username": stringSchema(),
		"version": integerSchema(math.MinInt64, math.MaxInt64), "version-hint": stringSchema(),
		"restls-script": stringSchema(), "force-tls12": boolSchema(), "alpn": alpnSchema(),
	}
	addClientTLSFields(fields)
	schema := objectSchema(fields)
	schema.validate = func(v policyValue, field string) error {
		fail := func(name string) error { return policyFailure(field + "." + name) }
		if !validClientKeyPair(v) {
			return fail("certificate")
		}
		mode, _ := v.get("mode")
		host, hasHost := v.get("host")
		switch mode.text {
		case "tls":
			// Native absent host defaults bing.com; an explicit empty host stays
			// empty. 212 + max first chunk16384 + host must fit uint16.
			if len(host.text) > 48939 {
				return fail("host")
			}
		case "http":
			if !validHTTPValue(host.text) {
				return fail("host")
			}
		case "shadow-tls":
			if !hasHost {
				return fail("host")
			}
			version, present := v.get("version")
			if present && (version.integer < 1 || version.integer > 3) {
				return fail("version")
			}
		case "restls":
			if !hasHost {
				return fail("host")
			}
			if _, present := v.get("password"); !present {
				return fail("password")
			}
			version, _ := v.get("version-hint")
			hint := strings.ToLower(version.text)
			if hint != "tls12" && hint != "tls13" {
				return fail("version-hint")
			}
			ceiling := 16372
			if hint == "tls12" {
				ceiling = 16364
			}
			script, _ := v.get("restls-script")
			if !validRestlsScript(script.text, ceiling) {
				return fail("restls-script")
			}
		case "jls":
			if host.text == "" {
				return fail("host")
			}
			for _, name := range []string{"username", "password"} {
				value, _ := v.get(name)
				if value.text == "" {
					return fail(name)
				}
			}
		}
		return nil
	}
	return schema
}
