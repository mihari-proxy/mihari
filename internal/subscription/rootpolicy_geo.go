package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/netip"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxGeoResourceBytes = 128 << 20

// GeoResourceKind selects one compiled global Geo asset; it is never a path.
type GeoResourceKind string

const (
	// GeoCountryMMDB contains country address classification data.
	GeoCountryMMDB GeoResourceKind = "country-mmdb"
	// GeoASNMMDB contains autonomous system address classification data.
	GeoASNMMDB GeoResourceKind = "asn-mmdb"
	// GeoIPDAT contains named IP category lists used in geodata mode.
	GeoIPDAT GeoResourceKind = "geoip-dat"
	// GeoSiteDAT contains named domain category lists and attributes.
	GeoSiteDAT GeoResourceKind = "geosite-dat"
)

// GeoResourceSpec contains validated bytes copied for one fixed core-home asset.
// SHA256 records the digest of Bytes; trusted source authentication belongs to T08.
type GeoResourceSpec struct {
	Kind   GeoResourceKind
	SHA256 string
	Bytes  []byte
}

// PolicyRequirements describes resources needed before producing executable YAML.
type PolicyRequirements struct {
	Providers []ProviderSpec
	Geo       []GeoResourceKind
}

// GeoResourcePath returns the only permitted core-home relative asset basename.
func GeoResourcePath(kind GeoResourceKind) (string, error) {
	switch kind {
	case GeoCountryMMDB:
		return "Country.mmdb", nil
	case GeoASNMMDB:
		return "ASN.mmdb", nil
	case GeoIPDAT:
		return "GeoIP.dat", nil
	case GeoSiteDAT:
		return "GeoSite.dat", nil
	default:
		return "", policyFailure("geo.kind")
	}
}

// GeoResourceID returns a reserved key outside the provider SHA256 namespace.
func GeoResourceID(kind GeoResourceKind) (string, error) {
	if _, err := GeoResourcePath(kind); err != nil {
		return "", err
	}
	return "mihari.geo/" + string(kind) + "/v1", nil
}

func validateGeoDAT(ctx context.Context, kind GeoResourceKind, input []byte, selectors []string, matcher string) (GeoResourceSpec, error) {
	if err := ctx.Err(); err != nil {
		return GeoResourceSpec{}, err
	}
	if len(input) == 0 || len(input) > maxGeoResourceBytes || (kind != GeoSiteDAT && kind != GeoIPDAT) {
		return GeoResourceSpec{}, policyFailure("geo.data")
	}
	entrySchema := geoIPWireSchema()
	if kind == GeoSiteDAT {
		entrySchema = geoSiteWireSchema()
	}
	entries, err := decodePolicyWire(ctx, input, map[uint32]policyWireSpec{1: {kind: wireMessage, repeated: true, message: entrySchema}}, "geo.data")
	if err != nil {
		return GeoResourceSpec{}, err
	}
	categories := make(map[string]bool)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return GeoResourceSpec{}, err
		}
		name, ok := wireGet(entry.message, 1)
		if !ok || name.text == "" || name.text != strings.TrimSpace(name.text) || strings.ContainsRune(name.text, 0) {
			return GeoResourceSpec{}, policyFailure("geo.category")
		}
		key := strings.ToLower(name.text)
		if categories[key] {
			return GeoResourceSpec{}, policyFailure("geo.category")
		}
		categories[key] = true
		for _, item := range entry.message {
			if item.number != 2 {
				continue
			}
			if kind == GeoSiteDAT {
				if !validGeoDomain(item.message, matcher) {
					return GeoResourceSpec{}, policyFailure("geo.domain")
				}
			} else {
				address, _ := wireGet(item.message, 1)
				prefix, _ := wireGet(item.message, 2)
				ip, ok := netip.AddrFromSlice(address.bytes)
				if !ok || prefix.unsigned > uint64(ip.BitLen()) {
					return GeoResourceSpec{}, policyFailure("geo.cidr")
				}
			}
		}
	}
	// Native InitGeo* validates CN even when another category is requested.
	if !categories["cn"] {
		return GeoResourceSpec{}, policyFailure("geo.category")
	}
	for _, selector := range selectors {
		key := strings.TrimPrefix(selector, "!")
		if kind == GeoSiteDAT {
			key = strings.TrimSpace(strings.SplitN(key, "@", 2)[0])
		}
		if key == "" || !categories[strings.ToLower(key)] {
			return GeoResourceSpec{}, policyFailure("geo.selector")
		}
	}
	digest := sha256.Sum256(input)
	return GeoResourceSpec{Kind: kind, SHA256: hex.EncodeToString(digest[:]), Bytes: append([]byte(nil), input...)}, nil
}

func geoIPWireSchema() map[uint32]policyWireSpec {
	return map[uint32]policyWireSpec{
		1: {kind: wireString},
		2: {kind: wireMessage, repeated: true, message: map[uint32]policyWireSpec{1: {kind: wireBytes}, 2: {kind: wireUnsigned, maxUint: math.MaxUint32}}},
		3: {kind: wireBool},
	}
}

func geoSiteWireSchema() map[uint32]policyWireSpec {
	return map[uint32]policyWireSpec{
		1: {kind: wireString},
		2: {kind: wireMessage, repeated: true, message: map[uint32]policyWireSpec{
			1: {kind: wireUnsigned, maxUint: 3}, 2: {kind: wireString},
			3: {kind: wireMessage, repeated: true, message: map[uint32]policyWireSpec{
				1: {kind: wireString}, 2: {kind: wireBool, oneof: 1},
				3: {kind: wireInt64, min: math.MinInt64, max: math.MaxInt64, oneof: 1},
			}},
		}},
	}
}

func validGeoDomain(fields []policyWireValue, matcher string) bool {
	typ, _ := wireGet(fields, 1)
	value, _ := wireGet(fields, 2)
	switch typ.unsigned {
	case 0:
		return true // substring matching, including the empty substring
	case 1:
		_, err := regexp.Compile(value.text)
		return err == nil
	case 2:
		if matcher == "mph" || matcher == "hybrid" {
			return true
		}
		return validPolicyDomain("+." + value.text)
	case 3:
		if matcher == "mph" || matcher == "hybrid" {
			return true
		}
		return validPolicyDomain(value.text)
	default:
		return false
	}
}

// validPolicyDomain implements the pinned DomainTrie pattern grammar, including
// whole-label wildcards; it is intentionally not a restrictive hostname regex.
func validPolicyDomain(value string) bool {
	if value == "" || strings.HasSuffix(value, ".") || !utf8.ValidString(value) {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return false
	}
	parts := strings.Split(strings.ToLower(value), ".")
	for i, part := range parts {
		if (i > 0 || len(parts) == 1) && part == "" {
			return false
		}
		if strings.Contains(part, "+") && (part != "+" || i != 0 || len(parts) == 1) {
			return false
		}
		if strings.Contains(part, "*") && part != "*" {
			return false
		}
	}
	return true
}
