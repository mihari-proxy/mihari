package subscription

import (
	"math"
	"net/netip"
	"strings"
	"time"
)

func stringSchema() *policySchema { return &policySchema{kind: policyString} }
func boolSchema() *policySchema   { return &policySchema{kind: policyBool} }
func integerSchema(min, max int64) *policySchema {
	return &policySchema{kind: policyInt, min: min, max: max}
}
func unsignedSchema(max uint64) *policySchema { return &policySchema{kind: policyUint, maxUint: max} }
func listSchema(element *policySchema) *policySchema {
	return &policySchema{kind: policyList, element: element}
}
func objectSchema(fields map[string]*policySchema) *policySchema {
	return &policySchema{kind: policyObject, fields: fields}
}
func enumSchema(choices ...string) *policySchema {
	return &policySchema{kind: policyString, choices: choices, foldString: true}
}

// rootSchema is compiled code; no downloaded schema can extend its fields.
func rootSchema() *policySchema {
	text := stringSchema()
	boolean := boolSchema()
	seconds := integerSchema(math.MinInt64/int64(time.Second), math.MaxInt64/int64(time.Second))
	mark := unsignedSchema(math.MaxUint32)
	noSystemClock := boolSchema()
	noSystemClock.check = func(v policyValue) bool { return !v.boolean }
	prefix := stringSchema()
	prefix.check = func(v policyValue) bool { _, err := netip.ParsePrefix(v.text); return err == nil }
	fields := map[string]*policySchema{
		"dns":             dnsSchema(),
		"hosts":           hostsSchema(),
		"sniffer":         snifferSchema(),
		"tun":             discardedTunSchema(),
		"proxies":         listSchema(rootProxySchema()),
		"proxy-providers": providerMapSchema("proxy"),
		"rule-providers":  providerMapSchema("rule"),
		"proxy-groups":    listSchema(groupSchema()),
		"rules":           policyRuleListSchema(),
		"sub-rules":       subRulesSchema(),
		// Values are checked, then replaced exclusively from Manager settings.
		"mixed-port":   unsignedSchema(math.MaxUint16),
		"bind-address": text, "allow-lan": boolean,
		"external-controller": text, "secret": text,
		"ipv6": boolean, "unified-delay": boolean,
		"mode":        enumSchema("rule", "global", "direct"),
		"log-level":   enumSchema("debug", "info", "warning", "error", "silent"),
		"inbound-tfo": boolean, "inbound-mptcp": boolean,
		"tcp-concurrent": boolean, "etag-support": boolean,
		"disable-keep-alive": boolean, "keep-alive-idle": seconds, "keep-alive-interval": seconds,
		"routing-mark": mark, "interface-name": text, "global-ua": text,
		"find-process-mode": enumSchema("strict", "always", "off"),
		"authentication":    listSchema(text), "skip-auth-prefixes": listSchema(prefix),
		"lan-allowed-ips": listSchema(prefix), "lan-disallowed-ips": listSchema(prefix),
		"geodata-mode": boolean, "geodata-loader": text, "geosite-matcher": text,
		"global-client-fingerprint": text,
		"experimental": objectSchema(map[string]*policySchema{
			"fingerprints": listSchema(text), "quic-go-disable-gso": boolean,
			"quic-go-disable-ecn": boolean, "dialer-ip4p-convert": boolean,
		}),
		"clash-for-android": objectSchema(map[string]*policySchema{"append-system-dns": boolean, "ui-subtitle-pattern": text}),
		"profile":           objectSchema(map[string]*policySchema{"store-selected": boolean, "store-fake-ip": boolean}),
		"geo-auto-update":   boolean, "geo-update-interval": integerSchema(math.MinInt64, math.MaxInt64),
		"geox-url": objectSchema(map[string]*policySchema{"geoip": text, "mmdb": text, "asn": text, "geosite": text}),
		"ntp": objectSchema(map[string]*policySchema{
			"enable": boolean, "server": text, "port": integerSchema(0, math.MaxUint16),
			"interval":     integerSchema(math.MinInt64, math.MaxInt64/int64(time.Minute)),
			"dialer-proxy": text, "write-to-system": noSystemClock,
		}),
	}
	// These are registered capability rejections, not a search for dangerous
	// words: every unregistered field still fails the closed object decoder.
	for _, name := range strings.Fields("port socks-port redir-port tproxy-port ss-config vmess-config external-controller-unix external-controller-pipe external-controller-tls external-controller-routing-mark external-controller-cors external-doh-server external-ui external-ui-name external-ui-url listeners tunnels tuic-server iptables tls") {
		fields[name] = &policySchema{kind: policyString, reject: true}
	}
	return objectSchema(fields)
}
