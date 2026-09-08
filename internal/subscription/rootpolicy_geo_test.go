package subscription

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestRootPolicy_GeoIdentityAndPath(t *testing.T) {
	for _, tc := range []struct {
		kind GeoResourceKind
		want string
	}{
		{GeoCountryMMDB, "mihari.geo/country-mmdb/v1"},
		{GeoASNMMDB, "mihari.geo/asn-mmdb/v1"},
		{GeoIPDAT, "mihari.geo/geoip-dat/v1"},
		{GeoSiteDAT, "mihari.geo/geosite-dat/v1"},
	} {
		id, err := GeoResourceID(tc.kind)
		if err != nil || id != tc.want {
			t.Fatalf("Geo ID differs from approved versioned contract: %q, %v", id, err)
		}
	}
	seen := map[string]bool{}
	for _, tc := range []struct {
		kind GeoResourceKind
		path string
	}{
		{GeoCountryMMDB, "Country.mmdb"}, {GeoASNMMDB, "ASN.mmdb"},
		{GeoIPDAT, "GeoIP.dat"}, {GeoSiteDAT, "GeoSite.dat"},
	} {
		path, err := GeoResourcePath(tc.kind)
		if err != nil {
			t.Fatal(err)
		}
		if path != tc.path {
			t.Fatal("Geo path does not match compiled basename")
		}
		id, err := GeoResourceID(tc.kind)
		if err != nil {
			t.Fatal(err)
		}
		if id == "" || seen[id] {
			t.Fatal("Geo identity is empty or collided")
		}
		seen[id] = true
		if raw, err := hex.DecodeString(id); err == nil && len(raw) == 32 {
			t.Fatal("Geo ID overlaps provider namespace")
		}
	}
	for _, unknown := range []GeoResourceKind{"", "../Country.mmdb", "geoip.metadb"} {
		if _, err := GeoResourcePath(unknown); err == nil {
			t.Fatal("unknown Geo path accepted")
		}
		if _, err := GeoResourceID(unknown); err == nil {
			t.Fatal("unknown Geo ID accepted")
		}
	}
}

func geoTestMessage(number uint32, body ...policyWireValue) policyWireValue {
	return policyWireValue{number: number, kind: wireMessage, message: body}
}
func geoTestText(number uint32, text string) policyWireValue {
	return policyWireValue{number: number, kind: wireString, text: text}
}
func geoTestUint(number uint32, n uint64) policyWireValue {
	return policyWireValue{number: number, kind: wireUnsigned, unsigned: n}
}

func geoSiteFixture() []byte {
	return encodePolicyWire([]policyWireValue{
		geoTestMessage(1, geoTestText(1, "CN"),
			geoTestMessage(2, geoTestUint(1, 2), geoTestText(2, "example.test"),
				geoTestMessage(3, geoTestText(1, "ads"), policyWireValue{number: 2, kind: wireBool, boolean: true}))),
		geoTestMessage(1, geoTestText(1, "network-test"),
			geoTestMessage(2, geoTestUint(1, 0), geoTestText(2, "keyword")),
			geoTestMessage(2, geoTestUint(1, 1), geoTestText(2, `^api\..*\.test$`)),
			geoTestMessage(2, geoTestUint(1, 3), geoTestText(2, "api.example.test"),
				geoTestMessage(3, geoTestText(1, "weight"), policyWireValue{number: 3, kind: wireInt64, integer: -1}))),
	})
}

func TestRootPolicy_GeoDATValidatesSelectorsAndCopiesBytes(t *testing.T) {
	input := geoSiteFixture()
	got, err := validateGeoDAT(context.Background(), GeoSiteDAT, input, []string{"!CN@ads", " network-test @weight", "CN@missing"}, "succinct")
	if err != nil {
		t.Fatalf("valid GeoSite data rejected: %v", err)
	}
	digest := sha256.Sum256(input)
	if got.Kind != GeoSiteDAT || got.SHA256 != hex.EncodeToString(digest[:]) || !bytes.Equal(got.Bytes, input) {
		t.Fatal("validated Geo resource missing bytes or digest")
	}
	input[0] ^= 1
	if bytes.Equal(got.Bytes, input) {
		t.Fatal("Geo resource retained mutable input")
	}
}

func TestRootPolicy_GeoDATRejectsBrokenSemantics(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      []policyWireValue
		selectors []string
	}{
		{"missing-cn", []policyWireValue{geoTestMessage(1, geoTestText(1, "US"))}, nil},
		{"category-collision", []policyWireValue{geoTestMessage(1, geoTestText(1, "CN")), geoTestMessage(1, geoTestText(1, "cn"))}, nil},
		{"missing-selector", []policyWireValue{geoTestMessage(1, geoTestText(1, "CN"))}, []string{"missing"}},
		{"empty-selector", []policyWireValue{geoTestMessage(1, geoTestText(1, "CN"))}, []string{"!@ads"}},
		{"bad-regex", []policyWireValue{geoTestMessage(1, geoTestText(1, "CN"), geoTestMessage(2, geoTestUint(1, 1), geoTestText(2, "[")))}, nil},
		{"bad-domain", []policyWireValue{geoTestMessage(1, geoTestText(1, "CN"), geoTestMessage(2, geoTestUint(1, 2), geoTestText(2, "a..test")))}, nil},
		{"unknown-domain-kind", []policyWireValue{geoTestMessage(1, geoTestText(1, "CN"), geoTestMessage(2, geoTestUint(1, 4), geoTestText(2, "example.test")))}, nil},
		{"attribute-oneof", []policyWireValue{geoTestMessage(1, geoTestText(1, "CN"), geoTestMessage(2, geoTestMessage(3, geoTestText(1, "ads"), policyWireValue{number: 2, kind: wireBool}, policyWireValue{number: 3, kind: wireInt64, integer: 1})))}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateGeoDAT(context.Background(), GeoSiteDAT, encodePolicyWire(tc.body), tc.selectors, "succinct"); err == nil {
				t.Fatal("invalid GeoSite semantics accepted")
			}
		})
	}
}

func TestRootPolicy_GeoIPDATAddressesAndPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ip     []byte
		prefix uint64
		valid  bool
	}{
		{"ipv4", []byte{192, 0, 2, 0}, 24, true}, {"ipv4-max", []byte{192, 0, 2, 1}, 32, true},
		{"ipv6", make([]byte, 16), 128, true}, {"default-route", []byte{0, 0, 0, 0}, 0, true},
		{"ipv4-overflow", []byte{192, 0, 2, 1}, 33, false}, {"ipv6-overflow", make([]byte, 16), 129, false},
		{"invalid-size", []byte{192, 0, 2}, 24, false}, {"uint32-overflow", []byte{192, 0, 2, 1}, 1 << 32, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := encodePolicyWire([]policyWireValue{geoTestMessage(1, geoTestText(1, "CN"), geoTestMessage(2, policyWireValue{number: 1, kind: wireBytes, bytes: tc.ip}, geoTestUint(2, tc.prefix)), policyWireValue{number: 3, kind: wireBool, boolean: true})})
			got, err := validateGeoDAT(context.Background(), GeoIPDAT, input, []string{"!CN"}, "")
			if tc.valid && (err != nil || !bytes.Equal(got.Bytes, input)) {
				t.Fatalf("valid GeoIP data rejected or changed: %v", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("invalid CIDR accepted")
			}
		})
	}
}

func TestRootPolicy_DomainTrieGrammar(t *testing.T) {
	for _, good := range []string{"example.test", "*.example.test", "sub.*.test", ".example.test", "+.example.test", "localhost", "_service._tcp.test"} {
		if !validPolicyDomain(good) {
			t.Fatal("valid trie pattern rejected")
		}
	}
	for _, bad := range []string{"", "a..test", "a.test.", " a.test", "a.test ", "+", "a.+.test", "a*b.test"} {
		if validPolicyDomain(bad) {
			t.Fatal("invalid trie pattern accepted")
		}
	}
}
