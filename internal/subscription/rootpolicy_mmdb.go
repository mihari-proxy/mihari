package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

func validateGeoMMDB(ctx context.Context, kind GeoResourceKind, input []byte) (out GeoResourceSpec, resultErr error) {
	if err := ctx.Err(); err != nil {
		return GeoResourceSpec{}, err
	}
	if len(input) == 0 || len(input) > maxGeoResourceBytes || (kind != GeoCountryMMDB && kind != GeoASNMMDB) {
		return GeoResourceSpec{}, policyFailure("geo.data")
	}
	reader, err := maxminddb.OpenBytes(input)
	if err != nil {
		return GeoResourceSpec{}, policyFailure("geo.data")
	}
	defer func() {
		if err := reader.Close(); err != nil && resultErr == nil {
			out = GeoResourceSpec{}
			resultErr = policyFailure("geo.data")
		}
	}()
	if err := reader.Verify(); err != nil {
		return GeoResourceSpec{}, policyFailure("geo.data")
	}
	meaningful := false
	for record := range reader.Networks() {
		if err := ctx.Err(); err != nil {
			return GeoResourceSpec{}, err
		}
		if record.Err() != nil {
			return GeoResourceSpec{}, policyFailure("geo.data")
		}
		var found bool
		if kind == GeoCountryMMDB {
			found, err = validMMDBCountry(record, reader.Metadata.DatabaseType)
		} else {
			found, err = validMMDBASN(record, reader.Metadata.DatabaseType)
		}
		if err != nil {
			return GeoResourceSpec{}, err
		}
		meaningful = meaningful || found
	}
	if !meaningful {
		return GeoResourceSpec{}, policyFailure("geo.kind")
	}
	digest := sha256.Sum256(input)
	return GeoResourceSpec{Kind: kind, SHA256: hex.EncodeToString(digest[:]), Bytes: append([]byte(nil), input...)}, nil
}

func validMMDBCountry(record maxminddb.Result, databaseType string) (bool, error) {
	var codes []string
	switch databaseType {
	case "sing-geoip":
		var code string
		if err := record.Decode(&code); err != nil {
			return false, policyFailure("geo.country")
		}
		codes = []string{code}
	case "Meta-geoip0":
		var code string
		if err := record.Decode(&code); err == nil {
			codes = []string{code}
		} else if err := record.Decode(&codes); err != nil {
			return false, policyFailure("geo.country")
		}
	default:
		var value struct {
			Country struct {
				Code string `maxminddb:"iso_code"`
			} `maxminddb:"country"`
		}
		if err := record.Decode(&value); err != nil {
			return false, policyFailure("geo.country")
		}
		codes = []string{value.Country.Code}
	}
	found := false
	for _, code := range codes {
		if !utf8.ValidString(code) {
			return false, policyFailure("geo.country")
		}
		found = found || code != ""
	}
	return found, nil
}

func validMMDBASN(record maxminddb.Result, databaseType string) (bool, error) {
	switch databaseType {
	case "GeoLite2-ASN", "DBIP-ASN-Lite (compat=GeoLite2-ASN)":
		var value struct {
			Number       *uint32 `maxminddb:"autonomous_system_number"`
			Organization string  `maxminddb:"autonomous_system_organization"`
		}
		if err := record.Decode(&value); err != nil || value.Number == nil || !utf8.ValidString(value.Organization) {
			return false, policyFailure("geo.asn")
		}
		return true, nil
	case "ipinfo generic_asn_free.mmdb":
		var value struct {
			ASN  string `maxminddb:"asn"`
			Name string `maxminddb:"name"`
		}
		if err := record.Decode(&value); err != nil || !strings.HasPrefix(value.ASN, "AS") || !utf8.ValidString(value.Name) {
			return false, policyFailure("geo.asn")
		}
		if _, err := strconv.ParseUint(strings.TrimPrefix(value.ASN, "AS"), 10, 32); err != nil {
			return false, policyFailure("geo.asn")
		}
		return true, nil
	default:
		return false, policyFailure("geo.kind")
	}
}
