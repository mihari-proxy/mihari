package subscription

import (
	"math"
	"net/url"
	"time"
)

func mkcpSchema() *policySchema {
	return objectSchema(map[string]*policySchema{
		"mtu": unsignedSchema(math.MaxUint32), "tti": unsignedSchema(1000), "uplink-capacity": unsignedSchema(4095), "downlink-capacity": unsignedSchema(4095),
		"congestion": boolSchema(), "write-buffer": unsignedSchema(math.MaxUint32), "read-buffer": unsignedSchema(math.MaxUint32), "seed": stringSchema(), "header": stringSchema(),
	})
}

func mekyaSchema() *policySchema {
	fields := map[string]*policySchema{"url": stringSchema(), "kcp": mkcpSchema()}
	for _, name := range []string{"h2-pool-size", "max-request-size", "max-write-size", "max-write-duration-ms", "max-simultaneous-write-connection", "packet-writing-buffer"} {
		fields[name] = integerSchema(math.MinInt64, math.MaxInt64)
	}
	for _, name := range []string{"max-write-delay", "polling-interval-initial"} {
		fields[name] = integerSchema(math.MinInt64/int64(time.Millisecond), math.MaxInt64/int64(time.Millisecond))
	}
	schema := objectSchema(fields)
	schema.validate = func(v policyValue, field string) error {
		pool, _ := v.get("h2-pool-size")
		// Supported root targets have 64-bit words; an interface slice element
		// occupies two words. This only proves backing-size representation, not
		// affordability of the core's eager transport construction (even at -t).
		if pool.integer >= 2 && pool.integer > math.MaxInt64/16 {
			return policyFailure(field + ".h2-pool-size")
		}
		return nil
	}
	return schema
}

func addVmessTransportFields(fields map[string]*policySchema) {
	method := stringSchema()
	method.check = func(v policyValue) bool { return v.text == "" || validHTTPToken(v.text) }
	host := stringSchema()
	host.check = func(v policyValue) bool { return validHTTPValue(v.text) }
	fields["http-opts"] = objectSchema(map[string]*policySchema{"method": method, "path": listSchema(stringSchema()), "headers": policyHeadersSchema(true)})
	fields["h2-opts"] = objectSchema(map[string]*policySchema{"host": listSchema(host), "path": stringSchema()})
	fields["ws-opts"], fields["grpc-opts"], fields["mkcp-opts"], fields["mekya-opts"] = websocketSchema(), grpcSchema(), mkcpSchema(), mekyaSchema()
	fields["tlsmirror-opts"] = tlsMirrorSchema()
}

func vmessProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	for _, name := range []string{"uuid", "network", "servername", "packet-encoding", "client-fingerprint"} {
		fields[name] = stringSchema()
	}
	for _, name := range []string{"udp", "tls", "packet-addr", "xudp", "global-padding", "authenticated-length"} {
		fields[name] = boolSchema()
	}
	fields["alterId"] = integerSchema(math.MinInt64, math.MaxInt64)
	fields["cipher"] = enumSchema("auto", "none", "zero", "aes-128-cfb", "aes-128-gcm", "chacha20-poly1305")
	fields["alpn"] = alpnSchema()
	addClientTLSFields(fields)
	addTLSModeFields(fields)
	fields["reality-opts"] = realitySchema()
	addVmessTransportFields(fields)
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "uuid", "alterId", "cipher")
	schema.validate = validateVmessTransports
	return schema
}

func validateVmessTransports(v policyValue, field string) error {
	if err := validateRealityTLSModes(v, field); err != nil {
		return err
	}
	active := 0
	tcpOnly := false
	for _, name := range []string{"shadow-tls-opts", "restls-opts", "jls-opts"} {
		mode, _ := v.get(name)
		for _, member := range mode.fields {
			if member.value.text != "" || member.value.integer != 0 {
				active++
				tcpOnly = true
				break
			}
		}
	}
	reality, _ := v.get("reality-opts")
	key, _ := reality.get("public-key")
	if key.text != "" {
		active++
	}
	mirror, _ := v.get("tlsmirror-opts")
	key, _ = mirror.get("primary-key")
	if key.text != "" {
		active++
	}
	if active > 1 {
		return policyFailure(field + ".security-modes")
	}
	tls, _ := v.get("tls")
	if active > 0 && !tls.boolean {
		return policyFailure(field + ".tls")
	}
	network, _ := v.get("network")
	if tcpOnly && (network.text == "mkcp" || network.text == "kcp") {
		return policyFailure(field + ".network")
	}
	if network.text == "mekya" {
		mekya, _ := v.get("mekya-opts")
		endpoint, _ := mekya.get("url")
		if endpoint.text != "" {
			u, err := url.Parse(endpoint.text)
			if err != nil || u.Host == "" || (u.Scheme != "" && u.Scheme != "https") {
				return policyFailure(field + ".mekya-opts.url")
			}
		}
	}
	return nil
}
