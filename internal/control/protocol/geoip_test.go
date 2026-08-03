package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestGeoIPDTOsHaveStableJSONContract(t *testing.T) {
	updated := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	status := GeoIPStatus{Schema: "mihari/v1", Revision: 4,
		Country: GeoIPDatabaseStatus{Available: true, UpdatedAt: updated},
		ASN:     GeoIPDatabaseStatus{Available: false, Error: "Unavailable"},
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"mihari/v1","revision":4,"country":{"available":true,"updated_at":"2026-08-03T12:00:00Z"},"asn":{"available":false,"error":"Unavailable"}}`
	if string(raw) != want {
		t.Fatalf("json=%s want=%s", raw, want)
	}
	lookup := GeoIPLookupResult{Schema: "mihari/v1", Records: []GeoIPRecord{{Address: "1.1.1.1", CountryCode: "AU", ASN: 13335, Organization: "Cloudflare, Inc."}}}
	if raw, err = json.Marshal(lookup); err != nil || string(raw) != `{"schema":"mihari/v1","records":[{"address":"1.1.1.1","country_code":"AU","asn":13335,"organization":"Cloudflare, Inc."}]}` {
		t.Fatalf("lookup=%s err=%v", raw, err)
	}
}
