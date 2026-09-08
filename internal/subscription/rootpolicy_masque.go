package subscription

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"math"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func masqueProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	for _, field := range []string{"private-key", "public-key", "ip", "ipv6", "uri", "sni", "name-cert-verify", "network", "congestion-controller", "bbr-profile"} {
		fields[field] = stringSchema()
	}
	for _, field := range []string{"udp", "skip-cert-verify", "remote-dns-resolve"} {
		fields[field] = boolSchema()
	}
	fields["mtu"], fields["cwnd"] = integerSchema(math.MinInt64, math.MaxInt64), integerSchema(math.MinInt64, math.MaxInt64)
	fields["handshake-timeout"] = integerSchema(0, math.MaxInt64/int64(time.Second))
	fields["ip-stack"], fields["dns"] = ipStackSchema(), listSchema(nameserverSchema())
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "private-key", "public-key")
	schema.validate = func(v policyValue, field string) error {
		fail := func(name string) error { return policyFailure(field + "." + name) }
		private, _ := v.get("private-key")
		der, err := base64.StdEncoding.DecodeString(private.text)
		if err != nil {
			return fail("private-key")
		}
		if _, err := x509.ParseECPrivateKey(der); err != nil {
			return fail("private-key")
		}
		public, _ := v.get("public-key")
		der, err = base64.StdEncoding.DecodeString(public.text)
		if err != nil {
			return fail("public-key")
		}
		key, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			return fail("public-key")
		}
		if _, ok := key.(*ecdsa.PublicKey); !ok {
			return fail("public-key")
		}
		network, _ := v.get("network")
		if network.text != "h2" {
			cc, _ := v.get("congestion-controller")
			cwnd, _ := v.get("cwnd")
			packetSize := int64(0)
			switch cc.text {
			case "bbr", "bbr_meta_v2":
				packetSize = 1242
			case "bbr_meta_v1":
				packetSize = 1280
			}
			if packetSize != 0 && (cwnd.integer < math.MinInt64/packetSize || cwnd.integer > math.MaxInt64/packetSize) {
				return fail("cwnd")
			}
		}
		if network.text == "h3-l4proxy" {
			return nil
		}
		uri, _ := v.get("uri")
		if uri.text != "" {
			// Both connect-IP callers reject every URI template variable. The
			// remaining literal becomes an HTTP request URL on the pinned carrier.
			parsed, err := url.Parse(uri.text)
			if err != nil || strings.ContainsAny(uri.text, "{}") || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return fail("uri")
			}
		}
		var prefixes []netip.Prefix
		for _, entry := range []struct {
			name string
			bits int
		}{{"ip", 32}, {"ipv6", 128}} {
			value, _ := v.get(entry.name)
			if value.text == "" {
				continue
			}
			text := value.text
			if !strings.Contains(text, "/") {
				text += "/" + strconv.Itoa(entry.bits)
			}
			prefix, err := netip.ParsePrefix(text)
			if err != nil {
				return fail(entry.name)
			}
			prefixes = append(prefixes, prefix)
		}
		if len(prefixes) == 0 {
			return fail("ip")
		}
		mtu, _ := v.get("mtu")
		effectiveMTU := mtu.integer
		if effectiveMTU == 0 {
			effectiveMTU = 1280
		}
		if effectiveMTU < 0 || effectiveMTU > math.MaxUint32 {
			return fail("mtu")
		}
		stack, _ := v.get("ip-stack")
		if !validPolicyIPStack(stack, prefixes, uint32(effectiveMTU)) {
			return fail("ip-stack")
		}
		return nil
	}
	return schema
}
