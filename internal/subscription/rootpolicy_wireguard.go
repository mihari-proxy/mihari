package subscription

import (
	"crypto/ecdh"
	"encoding/base64"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

func wireGuardPeerFields() map[string]*policySchema {
	reserved := listSchema(unsignedSchema(255))
	reserved.check = func(v policyValue) bool { return len(v.items) == 0 || len(v.items) == 3 }
	return map[string]*policySchema{
		"server": stringSchema(), "port": integerSchema(0, 65535),
		"public-key": stringSchema(), "pre-shared-key": stringSchema(),
		"reserved": reserved, "allowed-ips": listSchema(stringSchema()),
	}
}

func wireGuardProxySchema() *policySchema {
	fields := proxyBaseFields()
	for key, schema := range wireGuardPeerFields() {
		fields[key] = schema
	}
	for _, field := range []string{"private-key", "ip", "ipv6"} {
		fields[field] = stringSchema()
	}
	fields["workers"] = integerSchema(math.MinInt64, math.MaxInt64)
	fields["mtu"] = integerSchema(0, math.MaxUint32)
	fields["persistent-keepalive"] = integerSchema(0, 65535)
	fields["refresh-server-ip-interval"] = integerSchema(math.MinInt64/int64(time.Second), math.MaxInt64/int64(time.Second))
	fields["ip-stack"], fields["dns"] = ipStackSchema(), listSchema(nameserverSchema())
	fields["amnezia-wg-option"] = amneziaSchema()
	fields["udp"], fields["remote-dns-resolve"] = boolSchema(), boolSchema()
	peer := objectSchema(wireGuardPeerFields())
	peer.validate = func(v policyValue, field string) error { return validateWireGuardPeer(v, field, true) }
	fields["peers"] = listSchema(peer)
	schema := proxyObject(fields)
	schema.required = append(schema.required, "private-key")
	schema.validate = func(v policyValue, field string) error {
		private, _ := v.get("private-key")
		if !validWireGuardKey(private.text) {
			return policyFailure(field + ".private-key")
		}
		peers, _ := v.get("peers")
		if len(peers.items) == 0 {
			if err := validateWireGuardPeer(v, field, false); err != nil {
				return err
			}
		}
		if !validWireGuardWorkerReferences(v) {
			return policyFailure(field + ".workers")
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
				return policyFailure(field + "." + entry.name)
			}
			prefixes = append(prefixes, prefix)
		}
		if len(prefixes) == 0 {
			return policyFailure(field + ".ip")
		}
		mtu, _ := v.get("mtu")
		effective := mtu.integer
		if effective == 0 {
			effective = 1408
		}
		stack, _ := v.get("ip-stack")
		if !validPolicyIPStack(stack, prefixes, uint32(effective)) {
			return policyFailure(field + ".ip-stack")
		}
		return nil
	}
	return schema
}

func validWireGuardWorkerReferences(v policyValue) bool {
	workers, _ := v.get("workers")
	if workers.integer < 0 {
		return false
	}
	private, _ := v.get("private-key")
	bytes, err := base64.StdEncoding.DecodeString(private.text)
	if err != nil || len(bytes) != 32 {
		return false
	}
	var self [32]byte
	var nonzero byte
	for _, b := range bytes {
		nonzero |= b
	}
	if nonzero != 0 {
		key, err := ecdh.X25519().NewPrivateKey(bytes)
		if err != nil {
			return false
		}
		copy(self[:], key.PublicKey().Bytes())
	}
	// FromMaybeZeroHex preserves zero; fresh SetPrivateKey then returns early
	// and leaves its self public key zero. Standard X25519 derivation would
	// produce a different key for that special device configuration.
	peers, _ := v.get("peers")
	active := peers.items
	if len(active) == 0 {
		active = []policyValue{v}
	}
	distinct := make(map[[32]byte]struct{})
	for _, peer := range active {
		value, _ := peer.get("public-key")
		decoded, err := base64.StdEncoding.DecodeString(value.text)
		if err != nil || len(decoded) != 32 {
			return false
		}
		key := [32]byte(decoded)
		if key == self {
			continue
		}
		distinct[key] = struct{}{}
		if len(distinct) > 65536 {
			return false
		}
	}
	// The queue starts at one reference, then gains W workers, one TUN
	// reader, and each resident peer. This is representation only: the core
	// eagerly starts 3W workers, and this bound does not make that affordable.
	return workers.integer == 0 || workers.integer <= math.MaxInt32-2-int64(len(distinct))
}

func validWireGuardKey(value string) bool {
	decoded, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validateWireGuardPeer(v policyValue, field string, explicit bool) error {
	key, _ := v.get("public-key")
	if !validWireGuardKey(key.text) {
		return policyFailure(field + ".public-key")
	}
	shared, _ := v.get("pre-shared-key")
	if shared.text != "" && !validWireGuardKey(shared.text) {
		return policyFailure(field + ".pre-shared-key")
	}
	if explicit {
		allowed, _ := v.get("allowed-ips")
		if len(allowed.items) == 0 {
			return policyFailure(field + ".allowed-ips")
		}
		for _, item := range allowed.items {
			// Parsing closes the raw UAPI line grammar, including CR/LF, before
			// the pinned consumer formats allowed_ip=... records.
			if _, err := netip.ParsePrefix(item.text); err != nil {
				return policyFailure(field + ".allowed-ips[]")
			}
		}
	}
	return nil
}
