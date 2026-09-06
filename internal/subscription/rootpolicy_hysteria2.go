package subscription

import (
	"math"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func hysteria2ProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(0, 65535)
	for _, name := range []string{"ports", "hop-interval", "sni", "bbr-profile", "obfs-password"} {
		fields[name] = stringSchema()
	}
	password := stringSchema()
	password.check = func(v policyValue) bool { return validHTTPValue(v.text) }
	fields["password"] = password // Hysteria authentication is an HTTP/3 header.
	fields["obfs"] = &policySchema{kind: policyString, choices: []string{"", "salamander", "gecko"}}
	fields["up"], fields["down"] = rateSchema(), rateSchema()
	for _, name := range []string{"obfs-min-packet-size", "obfs-max-packet-size", "cwnd", "udp-mtu"} {
		fields[name] = integerSchema(math.MinInt64, math.MaxInt64)
	}
	fields["handshake-timeout"] = integerSchema(math.MinInt64/int64(time.Second), math.MaxInt64/int64(time.Second))
	fields["initial-stream-receive-window"], fields["initial-connection-receive-window"] = unsignedSchema((1<<62)-1), unsignedSchema((1<<62)-1)
	fields["max-stream-receive-window"], fields["max-connection-receive-window"] = unsignedSchema(math.MaxUint64), unsignedSchema(math.MaxUint64)
	fields["ech-opts"], fields["alpn"], fields["realm-opts"] = echSchema(), alpnSchema(), hysteria2RealmSchema()
	addClientTLSFields(fields)
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server")
	schema.validate = func(v policyValue, field string) error {
		fail := func(name string) error { return policyFailure(field + "." + name) }
		if !validClientKeyPair(v) {
			return fail("certificate")
		}
		cwnd, _ := v.get("cwnd")
		if !validBBRWindowNumber(cwnd.integer) {
			return fail("cwnd")
		}
		mtu, _ := v.get("udp-mtu")
		// Weak necessary sanity only. Fragmentation additionally depends on the
		// actual destination and a later peer/PMTU retry value (see policy doc).
		if mtu.integer != 0 && mtu.integer <= 12 {
			return fail("udp-mtu")
		}
		up, _ := v.get("up")
		send, _ := policyRate(up.text)
		if send > math.MaxInt64 {
			return fail("up")
		}
		obfs, _ := v.get("obfs")
		password, _ := v.get("obfs-password")
		if obfs.text != "" && password.text == "" {
			return fail("obfs-password")
		}
		if obfs.text == "gecko" {
			minSize, _ := v.get("obfs-min-packet-size")
			maxSize, _ := v.get("obfs-max-packet-size")
			low, high := minSize.integer, maxSize.integer
			if low == 0 {
				low = 512
			}
			if high == 0 {
				high = 1200
			}
			if low <= 0 || low > high {
				return fail("obfs-min-packet-size")
			}
			if high > 2048 {
				return fail("obfs-max-packet-size")
			}
		}
		ports, _ := v.get("ports")
		portCount, ok := policyUnsignedRangesCount(ports.text, 16)
		if !ok {
			return fail("ports")
		}
		port, _ := v.get("port")
		if port.integer == 0 && portCount == 0 {
			return fail("port")
		}
		realm, _ := v.get("realm-opts")
		enabled, _ := realm.get("enable")
		if enabled.boolean && portCount > 0 {
			return fail("realm-opts")
		}
		if portCount > 0 {
			hop, _ := v.get("hop-interval")
			low, high, ok := policyUnsignedRange(hop.text, 64)
			if !ok {
				return fail("hop-interval")
			}
			if low == 0 {
				low = 30
			} else if low < 5 {
				low = 5
			}
			if high < low {
				high = low
			}
			if high > uint64(math.MaxInt64/int64(time.Second)) {
				return fail("hop-interval")
			}
		}
		return nil
	}
	return schema
}

func hysteria2RealmSchema() *policySchema {
	fields := map[string]*policySchema{"enable": boolSchema(), "realm-id": stringSchema(), "sni": stringSchema(), "alpn": alpnSchema()}
	endpoint := stringSchema()
	endpoint.check = func(v policyValue) bool {
		if v.text == "" {
			return true
		}
		u, err := url.Parse(v.text)
		return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.Opaque == "" && validHTTPValue(u.Host)
	}
	fields["server-url"] = endpoint
	token := stringSchema()
	token.check = func(v policyValue) bool { return validHTTPValue(v.text) }
	fields["token"] = token
	stun := stringSchema()
	stun.check = func(v policyValue) bool {
		host, port, err := net.SplitHostPort(v.text)
		if err != nil {
			host, port = v.text, "3478"
		}
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return false
		}
		if _, err := netip.ParseAddr(host); err == nil {
			return true
		}
		return host != "" && !strings.ContainsAny(host, " /:\\[]\t\r\n\x00")
	}
	fields["stun-servers"] = listSchema(stun)
	addClientTLSFields(fields)
	schema := objectSchema(fields)
	schema.validate = func(v policyValue, field string) error {
		if !validClientKeyPair(v) {
			return policyFailure(field + ".certificate")
		}
		enabled, _ := v.get("enable")
		endpoint, _ := v.get("server-url")
		if enabled.boolean && endpoint.text == "" {
			return policyFailure(field + ".server-url")
		}
		return nil
	}
	return schema
}

// policyUnsignedRange preserves the pinned bracket/space and reversed-range
// grammar, while checking the destination width before the native narrowing cast.
func policyUnsignedRange(value string, bits int) (uint64, uint64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, true
	}
	parts := strings.Split(value, "-")
	if len(parts) > 2 {
		return 0, 0, false
	}
	low, err := strconv.ParseUint(strings.Trim(parts[0], "[ ]"), 10, bits)
	if err != nil {
		return 0, 0, false
	}
	high := low
	if len(parts) == 2 {
		high, err = strconv.ParseUint(strings.Trim(parts[1], "[ ]"), 10, bits)
		if err != nil {
			return 0, 0, false
		}
	}
	if low > high {
		low, high = high, low
	}
	return low, high, true
}

func policyUnsignedRangesCount(value string, bits int) (uint64, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "*" {
		return 0, true
	}
	parts := strings.Split(strings.ReplaceAll(value, ",", "/"), "/")
	if len(parts) > 28 {
		return 0, false
	}
	var count uint64
	for _, part := range parts {
		if part == "" {
			continue
		}
		low, high, ok := policyUnsignedRange(part, bits)
		if !ok || high-low == math.MaxUint64 || high-low+1 > math.MaxUint64-count {
			return 0, false
		}
		count += high - low + 1
	}
	return count, true
}
