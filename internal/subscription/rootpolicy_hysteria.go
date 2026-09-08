package subscription

import (
	"context"
	"encoding/base64"
	"math"
	"strconv"
	"strings"
	"time"
)

func hysteriaProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(0, 65535)
	for _, name := range []string{"ports", "protocol", "obfs-protocol", "auth", "auth-str", "obfs", "sni"} {
		fields[name] = stringSchema()
	}
	fields["up"], fields["down"] = rateSchema(), rateSchema()
	fields["up-speed"], fields["down-speed"] = integerSchema(0, math.MaxInt64/125000), integerSchema(0, math.MaxInt64/125000)
	for _, name := range []string{"recv-window-conn", "recv-window"} {
		fields[name] = integerSchema(math.MinInt64, math.MaxInt64)
	}
	fields["hop-interval"] = integerSchema(math.MinInt64/int64(time.Second), math.MaxInt64/int64(time.Second))
	fields["disable-mtu-discovery"], fields["fast-open"] = boolSchema(), boolSchema()
	fields["alpn"], fields["ech-opts"] = alpnSchema(), echSchema()
	addClientTLSFields(fields)
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "up", "down")
	schema.transform = func(_ context.Context, v policyValue, field string) (policyValue, error) {
		fail := func(name string) (policyValue, error) { return policyValue{}, policyFailure(field + "." + name) }
		if !validClientKeyPair(v) {
			return fail("certificate")
		}
		protocol, _ := v.get("protocol")
		alias, _ := v.get("obfs-protocol")
		if alias.text != "" {
			protocol = alias
		}
		if protocol.text == "" {
			protocol = policyValue{kind: policyString, text: "udp"}
		}
		if protocol.text != "udp" && protocol.text != "wechat-video" {
			return fail("protocol")
		}
		// Materialize precedence so forbidden faketcp cannot survive in output.
		v.set("protocol", protocol)
		v.remove("obfs-protocol")
		for _, name := range []string{"up", "down"} {
			rate, _ := v.get(name)
			bps, ok := policyRate(rate.text)
			if !ok || bps == 0 {
				return fail(name)
			}
		}
		auth, _ := v.get("auth")
		plain, _ := v.get("auth-str")
		if auth.text != "" {
			decoded, err := base64.StdEncoding.DecodeString(auth.text)
			if err != nil || len(decoded) > 65535 {
				return fail("auth")
			}
		} else if len(plain.text) > 65535 {
			return fail("auth-str")
		}
		window, _ := v.get("recv-window")
		stream, _ := v.get("recv-window-conn")
		// Both upstream defaults are gated by ReceiveWindow, including the stream
		// window. Keep that pinned behavior, and validate only effective initials.
		if window.integer != 0 {
			if window.integer < 0 || window.integer > (1<<62)-1 {
				return fail("recv-window")
			}
			if stream.integer < 0 || stream.integer > (1<<62)-1 {
				return fail("recv-window-conn")
			}
		}
		ports, _ := v.get("ports")
		hop, _ := v.get("hop-interval")
		if protocol.text == "udp" && ports.text != "" {
			if !validHysteriaPorts(ports.text) {
				return fail("ports")
			}
			if hop.integer < 0 {
				return fail("hop-interval")
			} // zero becomes ten seconds.
		}
		return v, nil
	}
	return schema
}

func validHysteriaPorts(value string) bool {
	for part := range strings.SplitSeq(value, ",") {
		if low, high, rangeFound := strings.Cut(part, "-"); rangeFound {
			if _, err := strconv.ParseUint(low, 10, 16); err != nil {
				return false
			}
			if _, err := strconv.ParseUint(high, 10, 16); err != nil {
				return false
			}
			// Reversed ranges are swapped by the core. Port zero is representable.
		} else if _, err := strconv.ParseUint(part, 10, 16); err != nil {
			return false
		}
	}
	return true
}
