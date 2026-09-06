package subscription

import (
	"math"
	"net/netip"
	"strings"
)

// discardedTunSchema accounts for every declared RawTun field. None of these
// values is emitted or acted on: subscription data cannot select an OS device,
// descriptor, route or TUN lifecycle. Its schema is distinct from Manager input.
func discardedTunSchema() *policySchema {
	fields := make(map[string]*policySchema)
	for _, name := range strings.Fields("enable auto-route auto-detect-interface gso auto-redirect strict-route endpoint-independent-nat disable-icmp-forwarding recvmsgx sendmsgx") {
		fields[name] = boolSchema()
	}
	fields["device"] = stringSchema()
	fields["stack"] = enumSchema("gvisor", "system", "mixed")
	for _, name := range strings.Fields("mtu gso-max-size auto-redirect-input-mark auto-redirect-output-mark") {
		fields[name] = unsignedSchema(math.MaxUint32)
	}
	for _, name := range strings.Fields("iproute2-table-index iproute2-rule-index auto-redirect-iproute2-fallback-rule-index udp-timeout icmp-timeout file-descriptor") {
		fields[name] = integerSchema(math.MinInt64, math.MaxInt64)
	}
	for _, name := range strings.Fields("dns-hijack route-address-set route-exclude-address-set include-interface exclude-interface include-uid-range exclude-uid-range exclude-src-port-range exclude-dst-port-range include-package exclude-package include-mac-address exclude-mac-address") {
		fields[name] = listSchema(stringSchema())
	}
	for _, name := range []string{"include-uid", "exclude-uid"} {
		fields[name] = listSchema(unsignedSchema(math.MaxUint32))
	}
	for _, name := range []string{"exclude-src-port", "exclude-dst-port"} {
		fields[name] = listSchema(unsignedSchema(math.MaxUint16))
	}
	fields["include-android-user"] = listSchema(integerSchema(math.MinInt64, math.MaxInt64))
	prefix := stringSchema()
	prefix.check = func(v policyValue) bool { _, err := netip.ParsePrefix(v.text); return err == nil }
	for _, name := range strings.Fields("inet6-address route-address route-exclude-address inet4-route-address inet6-route-address inet4-route-exclude-address inet6-route-exclude-address") {
		fields[name] = listSchema(prefix)
	}
	address := stringSchema()
	address.check = func(v policyValue) bool { _, err := netip.ParseAddr(v.text); return err == nil }
	fields["loopback-address"] = listSchema(address)
	return objectSchema(fields)
}

// policyManagedTun validates the exact fields produced by runtime.buildManagedTun.
// It never marshals an arbitrary Settings map or copies additional members into
// the candidate. The old portable/Windows Settings contract remains unchanged.
func policyManagedTun(input map[string]any) (policyValue, error) {
	result := policyValue{kind: policyObject}
	result.set("enable", policyValue{kind: policyBool})
	for name, raw := range input {
		switch name {
		case "enable":
			value, ok := raw.(bool)
			if !ok {
				return policyValue{}, policyFailure("settings.tun.enable")
			}
			result.set("enable", policyValue{kind: policyBool, boolean: value})
		case "stack":
			value, ok := raw.(string)
			if !ok {
				return policyValue{}, policyFailure("settings.tun.stack")
			}
			value = strings.ToLower(value)
			if value != "gvisor" && value != "system" && value != "mixed" {
				return policyValue{}, policyFailure("settings.tun.stack")
			}
			result.set("stack", policyValue{kind: policyString, text: value})
		default:
			return policyValue{}, policyFailure("settings.tun.[unknown]")
		}
	}
	return result, nil
}
