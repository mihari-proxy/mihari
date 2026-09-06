package subscription

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func TestRootPolicy_WirePreservesKnownTypedFields(t *testing.T) {
	schema := map[uint32]policyWireSpec{
		1: {kind: wireString}, 2: {kind: wireBool},
		3: {kind: wireMessage, repeated: true, message: map[uint32]policyWireSpec{1: {kind: wireUnsigned, maxUint: 255}}},
	}
	input := []byte{0x0a, 2, 'c', 'n', 0x10, 1, 0x1a, 3, 8, 0xff, 1, 0x1a, 2, 8, 7}
	got, err := decodePolicyWire(context.Background(), input, schema, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].text != "cn" || !got[1].boolean || len(got[2].message) != 1 || got[2].message[0].unsigned != 255 || got[3].message[0].unsigned != 7 {
		t.Fatal("typed protobuf content/order lost")
	}
	input[2] = 'x'
	if got[0].text != "cn" {
		t.Fatal("wire decoder retained source")
	}
}

func TestRootPolicy_WireRejectsMalformedFrames(t *testing.T) {
	schema := map[uint32]policyWireSpec{
		1: {kind: wireString}, 2: {kind: wireBool},
		3: {kind: wireUnsigned, maxUint: 12},
		4: {kind: wireInt32, min: math.MinInt32, max: math.MaxInt32},
		5: {kind: wireBool, oneof: 1}, 6: {kind: wireInt32, min: 0, max: 255, oneof: 1},
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"zero-tag", []byte{0}}, {"unknown", []byte{56, 0}},
		{"truncated-tag", []byte{128}}, {"overflow-tag", bytes.Repeat([]byte{255}, 11)},
		{"fixed32", []byte{13, 0, 0, 0, 0}}, {"fixed64", []byte{9, 0, 0, 0, 0, 0, 0, 0, 0}},
		{"group", []byte{11, 12}}, {"length-overflow", append([]byte{10}, bytes.Repeat([]byte{255}, 11)...)},
		{"length-past-end", []byte{10, 2, 'a'}}, {"invalid-utf8", []byte{10, 1, 255}},
		{"duplicate-singular", []byte{16, 1, 16, 0}}, {"nonboolean", []byte{16, 2}},
		{"unsigned-boundary", []byte{24, 13}}, {"int32-overflow", binary.AppendUvarint([]byte{32}, 1<<31)},
		{"oneof-conflict", []byte{40, 1, 48, 12}}, {"bad-wire-type", []byte{18, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodePolicyWire(context.Background(), tc.data, schema, "fixture"); err == nil {
				t.Fatal("malformed wire accepted")
			}
		})
	}
}

func TestRootPolicy_WireRegeneratesSignedAndNoncanonicalValues(t *testing.T) {
	schema := map[uint32]policyWireSpec{1: {kind: wireInt32, min: math.MinInt32, max: math.MaxInt32}, 2: {kind: wireBool}}
	input := binary.AppendUvarint([]byte{8}, math.MaxUint64)
	input = append(input, 16, 0x81, 0)
	got, err := decodePolicyWire(context.Background(), input, schema, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].integer != -1 || !got[1].boolean {
		t.Fatal("signed or overlong value changed")
	}
	encoded := encodePolicyWire(got)
	want := append(binary.AppendUvarint([]byte{8}, math.MaxUint64), 16, 1)
	if !bytes.Equal(encoded, want) {
		t.Fatal("output was not canonical known wire data")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := decodePolicyWire(ctx, input, schema, "fixture"); !errors.Is(err, context.Canceled) {
		t.Fatal("wire cancellation ignored")
	}
}
