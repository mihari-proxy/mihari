package subscription

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestRootPolicy_MMDBVerifiesTypeAndCopiesBytes(t *testing.T) {
	for _, tc := range []struct {
		kind GeoResourceKind
		file string
	}{
		{GeoCountryMMDB, "country.mmdb"}, {GeoASNMMDB, "asn.mmdb"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			input, err := os.ReadFile("testdata/rootpolicy/mmdb/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			got, err := validateGeoMMDB(context.Background(), tc.kind, input)
			if err != nil {
				t.Fatalf("official MMDB fixture rejected: %v", err)
			}
			digest := sha256.Sum256(input)
			if got.Kind != tc.kind || !bytes.Equal(got.Bytes, input) || got.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatal("validated MMDB copy/digest missing")
			}
			input[0] ^= 1
			if bytes.Equal(got.Bytes, input) {
				t.Fatal("MMDB output retained source bytes")
			}
		})
	}
}

func TestRootPolicy_MMDBRejectsMissingCorruptAndWrongKind(t *testing.T) {
	country, err := os.ReadFile("testdata/rootpolicy/mmdb/country.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	asn, err := os.ReadFile("testdata/rootpolicy/mmdb/asn.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		kind  GeoResourceKind
		input []byte
	}{
		{"missing", GeoCountryMMDB, nil}, {"garbage", GeoCountryMMDB, []byte("not-an-mmdb")},
		{"truncated", GeoCountryMMDB, country[:len(country)-1]},
		{"country-as-asn", GeoASNMMDB, country}, {"asn-as-country", GeoCountryMMDB, asn},
		{"dat-kind", GeoIPDAT, country},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateGeoMMDB(context.Background(), tc.kind, tc.input); err == nil {
				t.Fatal("invalid MMDB resource accepted")
			}
		})
	}
}
