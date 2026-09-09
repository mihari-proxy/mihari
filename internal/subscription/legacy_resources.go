package subscription

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type providerIdentity struct {
	Schema         string `json:"schema"`
	SubscriptionID string `json:"subscription_id"`
	Generation     uint64 `json:"generation"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
}

// ProviderResourceID returns the versioned identity hash used for every provider
// source kind. Names remain data and never become filesystem components.
func ProviderResourceID(subscriptionID string, generation uint64, kind, name string) (string, error) {
	if !profileIDPattern.MatchString(subscriptionID) {
		return "", dataError("invalid legacy provider identity")
	}
	if generation == 0 {
		return "", dataError("invalid legacy provider identity")
	}
	if kind != "proxy" && kind != "rule" {
		return "", dataError("invalid legacy provider identity")
	}
	if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
		return "", dataError("invalid legacy provider identity")
	}
	encoded, err := json.Marshal(providerIdentity{
		Schema: "mihari.provider-identity/v1", SubscriptionID: subscriptionID,
		Generation: generation, Kind: kind, Name: name,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// legacyProviderIdentity contains only fields that authorize historical WAL targets.
type legacyProviderIdentity struct {
	SubscriptionID                 string
	Generation                     uint64
	Kind, Name, ResourceID, Format string
}

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
		return "", dataError("invalid legacy Geo kind")
	}
}
