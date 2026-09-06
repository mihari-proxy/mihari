package subscription

import (
	"math"
	"strings"
	"time"
)

func tuicProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	for _, name := range []string{"token", "uuid", "password", "ip", "udp-relay-mode", "congestion-controller", "bbr-profile", "sni"} {
		fields[name] = stringSchema()
	}
	for _, name := range []string{"reduce-rtt", "disable-sni", "fast-open", "disable-mtu-discovery", "udp-over-stream"} {
		fields[name] = boolSchema()
	}
	for _, name := range []string{"cwnd", "max-open-streams", "max-udp-relay-packet-size", "max-datagram-frame-size"} {
		fields[name] = integerSchema(math.MinInt64, math.MaxInt64)
	}
	fields["heartbeat-interval"] = integerSchema(math.MinInt64, math.MaxInt64/int64(time.Millisecond))
	fields["request-timeout"] = integerSchema(math.MinInt64/int64(time.Millisecond), math.MaxInt64/int64(time.Millisecond))
	fields["recv-window"], fields["recv-window-conn"] = integerSchema(0, (1<<62)-1), integerSchema(0, (1<<62)-1)
	fields["udp-over-stream-version"] = integerSchema(0, 2)
	fields["ech-opts"], fields["alpn"] = echSchema(), alpnSchema()
	addClientTLSFields(fields)
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port")
	schema.validate = func(v policyValue, field string) error {
		fail := func(name string) error { return policyFailure(field + "." + name) }
		if !validClientKeyPair(v) {
			return fail("certificate")
		}
		if !validInitialBBRWindow(v) {
			return fail("cwnd")
		}
		streams, _ := v.get("max-open-streams")
		count := streams.integer
		if count == 0 {
			count = 100
		}
		// Match the native floating-point ceil, including its rounding at large
		// integers, then check the actual signed addition before QUIC's clamp.
		growth := int64(math.Ceil(float64(count) / 10.0))
		if (growth > 0 && count > math.MaxInt64-growth) || (growth < 0 && count < math.MinInt64-growth) {
			return fail("max-open-streams")
		}
		frame, _ := v.get("max-datagram-frame-size")
		size := frame.integer
		if size == 0 {
			packet, _ := v.get("max-udp-relay-packet-size")
			size = packet.integer
			if size == 0 {
				size = 1252
			}
			if size > math.MaxInt64-27 {
				return fail("max-udp-relay-packet-size")
			}
			size += 27
		}
		if size > 1400 {
			size = 1400
		}
		if size < -1 {
			return fail("max-datagram-frame-size")
		}
		token, _ := v.get("token")
		relay, _ := v.get("udp-relay-mode")
		overStream, _ := v.get("udp-over-stream")
		if token.text == "" && relay.text != "quic" && !overStream.boolean && size < 28 {
			return fail("max-datagram-frame-size")
		}
		return nil
	}
	return schema
}

func shadowQUICProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	for _, name := range []string{"username", "password", "sni", "congestion-controller", "bbr-profile"} {
		fields[name] = stringSchema()
	}
	for _, name := range []string{"udp-over-stream", "zero-rtt", "disable-mtu-discovery"} {
		fields[name] = boolSchema()
	}
	fields["alpn"] = alpnSchema()
	version := stringSchema()
	version.check = func(v policyValue) bool {
		switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(v.text)), "_", "-") {
		case "v1", "1", "rfc9000", "rfc-9000", "v2", "2", "rfc9369", "rfc-9369":
			return true
		default:
			return false
		}
	}
	fields["quic-versions"] = listSchema(version)
	fields["keep-alive-interval"] = integerSchema(math.MinInt64, math.MaxInt64/int64(time.Millisecond))
	for _, name := range []string{"recv-window-conn", "recv-window"} {
		fields[name] = integerSchema(0, (1<<62)-1)
	}
	fields["max-datagram-frame-size"] = integerSchema(-1, (1<<62)-1)
	for _, name := range []string{"cwnd", "max-open-streams"} {
		fields[name] = integerSchema(math.MinInt64, math.MaxInt64)
	}
	fields["up"], fields["down"] = rateSchema(), rateSchema()
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port")
	schema.validate = func(v policyValue, field string) error {
		up, _ := v.get("up")
		down, _ := v.get("down")
		send, _ := policyRate(up.text)
		receive, _ := policyRate(down.text)
		if send > math.MaxInt64 {
			return policyFailure(field + ".up")
		}
		cwnd, _ := v.get("cwnd")
		if !validInitialBBRWindow(v) || ((send > 0 || receive > 0) && !validBBRWindowNumber(cwnd.integer)) {
			return policyFailure(field + ".cwnd")
		}
		return nil
	}
	return schema
}

func trustTunnelProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	for _, name := range []string{"username", "password", "sni", "client-fingerprint", "congestion-controller", "bbr-profile"} {
		fields[name] = stringSchema()
	}
	for _, name := range []string{"udp", "health-check", "quic"} {
		fields[name] = boolSchema()
	}
	for _, name := range []string{"cwnd", "max-connections", "min-streams", "max-streams"} {
		fields[name] = integerSchema(math.MinInt64, math.MaxInt64)
	}
	fields["alpn"], fields["ech-opts"] = alpnSchema(), echSchema()
	addClientTLSFields(fields)
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port")
	schema.validate = func(v policyValue, field string) error {
		if !validClientKeyPair(v) {
			return policyFailure(field + ".certificate")
		}
		quic, _ := v.get("quic")
		alpn, _ := v.get("alpn")
		required := "h2"
		if quic.boolean {
			required = "h3"
		}
		if len(alpn.items) > 0 {
			found := false
			for _, item := range alpn.items {
				if item.text == required {
					found = true
				}
			}
			if !found {
				return policyFailure(field + ".alpn")
			}
		}
		if quic.boolean && !validInitialBBRWindow(v) {
			return policyFailure(field + ".cwnd")
		}
		return nil
	}
	return schema
}

// These adapters use the native default initial packet size 1280. This proves
// only the constructor's first signed multiplication, not all BBR runtime math.
func validInitialBBRWindow(v policyValue) bool {
	cc, _ := v.get("congestion-controller")
	cwnd, _ := v.get("cwnd")
	switch cc.text {
	case "bbr", "bbr_meta_v1", "bbr_meta_v2":
		return validBBRWindowNumber(cwnd.integer)
	default:
		return true // Empty / unknown leaves native QUIC congestion unchanged.
	}
}

func validBBRWindowNumber(value int64) bool {
	return value >= math.MinInt64/1280 && value <= math.MaxInt64/1280
}
