package subscription

import (
	"context"
	"encoding/binary"
	"math"
	"unicode/utf8"
)

type policyWireKind uint8

const (
	wireUnsigned policyWireKind = iota
	wireInt32
	wireInt64
	wireBool
	wireString
	wireBytes
	wireMessage
)

type policyWireSpec struct {
	kind     policyWireKind
	repeated bool
	oneof    uint8
	min, max int64
	maxUint  uint64
	message  map[uint32]policyWireSpec
}
type policyWireValue struct {
	number   uint32
	kind     policyWireKind
	unsigned uint64
	integer  int64
	boolean  bool
	text     string
	bytes    []byte
	message  []policyWireValue
}

// decodePolicyWire handles only the finite schemas used by Geo and Mieru.
func decodePolicyWire(ctx context.Context, input []byte, schema map[uint32]policyWireSpec, field string) ([]policyWireValue, error) {
	var values []policyWireValue
	seen, oneofs := make(map[uint32]bool), make(map[uint8]bool)
	for len(input) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tag, n := binary.Uvarint(input)
		if n <= 0 || tag>>3 == 0 || tag>>3 > (1<<29)-1 {
			return nil, policyFailure(field)
		}
		input = input[n:]
		number := uint32(tag >> 3)
		spec, ok := schema[number]
		if !ok || (seen[number] && !spec.repeated) || (spec.oneof != 0 && oneofs[spec.oneof]) {
			return nil, policyFailure(field)
		}
		seen[number] = true
		if spec.oneof != 0 {
			oneofs[spec.oneof] = true
		}
		value := policyWireValue{number: number, kind: spec.kind}
		switch spec.kind {
		case wireUnsigned, wireInt32, wireInt64, wireBool:
			if tag&7 != 0 {
				return nil, policyFailure(field)
			}
			integer, count := binary.Uvarint(input)
			if count <= 0 {
				return nil, policyFailure(field)
			}
			input = input[count:]
			switch spec.kind {
			case wireUnsigned:
				if integer > spec.maxUint {
					return nil, policyFailure(field)
				}
				value.unsigned = integer
			case wireInt32, wireInt64:
				// Negative int32 values use the protobuf ten-byte sign extension.
				signed := int64(integer)
				if (spec.kind == wireInt32 && (signed < math.MinInt32 || signed > math.MaxInt32)) || signed < spec.min || signed > spec.max {
					return nil, policyFailure(field)
				}
				value.integer = signed
			case wireBool:
				if integer > 1 {
					return nil, policyFailure(field)
				}
				value.boolean = integer == 1
			}
		case wireString, wireBytes, wireMessage:
			if tag&7 != 2 {
				return nil, policyFailure(field)
			}
			length, count := binary.Uvarint(input)
			if count <= 0 {
				return nil, policyFailure(field)
			}
			input = input[count:]
			if length > uint64(len(input)) {
				return nil, policyFailure(field)
			}
			payload := input[:int(length)]
			input = input[int(length):]
			switch spec.kind {
			case wireString:
				if !utf8.Valid(payload) {
					return nil, policyFailure(field)
				}
				value.text = string(payload)
			case wireBytes:
				value.bytes = append([]byte(nil), payload...)
			case wireMessage:
				var err error
				value.message, err = decodePolicyWire(ctx, payload, spec.message, field)
				if err != nil {
					return nil, err
				}
			}
		default:
			return nil, policyFailure(field)
		}
		values = append(values, value)
	}
	return values, nil
}

func encodePolicyWire(values []policyWireValue) []byte {
	var out []byte
	for _, value := range values {
		switch value.kind {
		case wireUnsigned, wireInt32, wireInt64, wireBool:
			out = binary.AppendUvarint(out, uint64(value.number)<<3)
			var integer uint64
			switch value.kind {
			case wireUnsigned:
				integer = value.unsigned
			case wireInt32, wireInt64:
				integer = uint64(value.integer)
			case wireBool:
				if value.boolean {
					integer = 1
				}
			}
			out = binary.AppendUvarint(out, integer)
		case wireString, wireBytes, wireMessage:
			out = binary.AppendUvarint(out, uint64(value.number)<<3|2)
			var payload []byte
			switch value.kind {
			case wireString:
				payload = []byte(value.text)
			case wireBytes:
				payload = value.bytes
			case wireMessage:
				payload = encodePolicyWire(value.message)
			}
			out = binary.AppendUvarint(out, uint64(len(payload)))
			out = append(out, payload...)
		}
	}
	return out
}

func wireGet(values []policyWireValue, number uint32) (policyWireValue, bool) {
	for _, value := range values {
		if value.number == number {
			return value, true
		}
	}
	return policyWireValue{}, false
}
