package subscription

import (
	"os"
	"testing"
)

// addPolicyGeoFixture supplies explicit valid dependencies for field fixtures.
// Missing-resource tests intentionally do not call this helper.
func addPolicyGeoFixture(t *testing.T, input *PolicyInput, kind GeoResourceKind) {
	t.Helper()
	var data []byte
	var err error
	switch kind {
	case GeoCountryMMDB:
		data, err = os.ReadFile("testdata/rootpolicy/mmdb/country.mmdb")
	case GeoASNMMDB:
		data, err = os.ReadFile("testdata/rootpolicy/mmdb/asn.mmdb")
	case GeoSiteDAT:
		data = geoSiteFixture()
	default:
		t.Fatal("fixture kind not implemented")
	}
	if err != nil {
		t.Fatal(err)
	}
	id, err := GeoResourceID(kind)
	if err != nil {
		t.Fatal(err)
	}
	if input.Resources == nil {
		input.Resources = make(map[string][]byte)
	}
	input.Resources[id] = data
}
