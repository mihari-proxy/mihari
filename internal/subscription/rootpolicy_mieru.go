package subscription

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
)

func mieruProxySchema() *policySchema {
	fields := proxyBaseFields()
	// Mieru validates ProfileName before constructing its in-memory client
	// profile. This nonempty constraint is specific to that consumer.
	fields["name"] = stringSchema()
	fields["name"].check = func(v policyValue) bool { return v.text != "" }
	fields["server"], fields["username"], fields["password"] = policyNameSchema(), policyNameSchema(), policyNameSchema()
	fields["port"], fields["udp"] = integerSchema(0, 65535), boolSchema()
	// These protobuf map keys are exact and case-sensitive in the adapter.
	fields["transport"] = &policySchema{kind: policyString, choices: []string{"TCP", "UDP"}}
	fields["multiplexing"] = &policySchema{kind: policyString, choices: []string{"", "MULTIPLEXING_DEFAULT", "MULTIPLEXING_OFF", "MULTIPLEXING_LOW", "MULTIPLEXING_MIDDLE", "MULTIPLEXING_HIGH"}}
	fields["handshake-mode"] = &policySchema{kind: policyString, choices: []string{"", "HANDSHAKE_DEFAULT", "HANDSHAKE_STANDARD", "HANDSHAKE_NO_WAIT"}}
	ports := stringSchema()
	ports.check = func(v policyValue) bool {
		if v.text == "" {
			return true
		}
		low, high, ok := strings.Cut(v.text, "-")
		if !ok {
			return false
		}
		begin, e1 := strconv.ParseUint(low, 10, 16)
		end, e2 := strconv.ParseUint(high, 10, 16)
		return e1 == nil && e2 == nil && begin > 0 && begin <= end
	}
	fields["port-range"] = ports
	pattern := stringSchema()
	pattern.transform = func(ctx context.Context, v policyValue, field string) (policyValue, error) {
		encoded, err := validateMieruPattern(ctx, v.text)
		if err != nil {
			if ctx.Err() != nil {
				return policyValue{}, ctx.Err()
			}
			return policyValue{}, policyFailure(field)
		}
		v.text = encoded
		return v, nil
	}
	fields["traffic-pattern"] = pattern
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "transport", "username", "password")
	schema.validate = func(v policyValue, field string) error {
		port, _ := v.get("port")
		ports, _ := v.get("port-range")
		if (port.integer != 0) == (ports.text != "") {
			return policyFailure(field + ".port-range")
		}
		return nil
	}
	return schema
}

// validateMieruPattern implements Mieru v3.35.0's TrafficPattern proto and
// apis/trafficpattern.Validate. It cannot embed the unrelated ClientConfig proto.
func validateMieruPattern(ctx context.Context, encoded string) (string, error) {
	const field = "proxies[].traffic-pattern"
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(encoded) > maxDocumentBytes {
		return "", policyFailure(field)
	}
	input, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", policyFailure(field)
	}
	values, err := decodePolicyWire(ctx, input, mieruPatternWireSchema(), field)
	if err != nil {
		return "", err
	}
	if nonce, ok := wireGet(values, 4); ok {
		min, hasMin := wireGet(nonce.message, 3)
		max, hasMax := wireGet(nonce.message, 4)
		if hasMin && hasMax && min.integer > max.integer {
			return "", policyFailure(field + ".nonce")
		}
		for _, value := range nonce.message {
			if value.number != 5 {
				continue
			}
			raw, err := hex.DecodeString(value.text)
			if err != nil || len(raw) > 12 {
				return "", policyFailure(field + ".nonce")
			}
		}
	}
	if entropy, ok := wireGet(values, 6); ok {
		if rotation, ok := wireGet(entropy.message, 2); ok && rotation.unsigned > 15 && rotation.unsigned%16 != 0 {
			return "", policyFailure(field + ".entropy")
		}
	}
	return base64.StdEncoding.EncodeToString(encodePolicyWire(values)), nil
}

func mieruPatternWireSchema() map[uint32]policyWireSpec {
	return map[uint32]policyWireSpec{
		1: {kind: wireInt32, min: math.MinInt32, max: math.MaxInt32},
		2: {kind: wireBool},
		3: {kind: wireMessage, message: map[uint32]policyWireSpec{
			1: {kind: wireBool}, 2: {kind: wireInt32, min: 0, max: 100},
		}},
		4: {kind: wireMessage, message: map[uint32]policyWireSpec{
			1: {kind: wireUnsigned, maxUint: 3}, 2: {kind: wireBool},
			3: {kind: wireInt32, min: 0, max: 12}, 4: {kind: wireInt32, min: 0, max: 12},
			// This is the pattern's only repeated field. Bound its typed nodes
			// before allocation independently of the 16 MiB encoded byte limit.
			5: {kind: wireString, repeated: true, maxCount: 1 << 16},
		}},
		5: {kind: wireMessage, message: map[uint32]policyWireSpec{
			1: {kind: wireInt32, min: 0, max: 255}, 2: {kind: wireInt32, min: 0, max: 255},
		}},
		6: {kind: wireMessage, message: map[uint32]policyWireSpec{
			1: {kind: wireUnsigned, maxUint: 4}, 2: {kind: wireUnsigned, maxUint: 240},
		}},
	}
}
