package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestGeoIPRoutesExposeStatusLookupAndCoordinatedUpdate(t *testing.T) {
	fake := &fakeRuntime{
		snapshot:     state.Snapshot{Revision: 8},
		geoIPStatus:  geoip.Status{Country: geoip.DatabaseStatus{Available: true, LastError: "update failed"}, ASN: geoip.DatabaseStatus{Available: true}},
		geoIPRecords: []geoip.Record{{CountryCode: "AU", ASN: 13335, Organization: "Cloudflare, Inc."}},
	}
	server := New(Options{Token: "token", Store: state.NewStore(fake.snapshot), Runtime: fake})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/geoip/status", nil))
	var status protocol.GeoIPStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || status.Country.Error != "Update failed" {
		t.Fatalf("status=%#v err=%v", status, err)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/geoip/lookup", bytes.NewBufferString(`{"addresses":["1.1.1.1"]}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("lookup status=%d body=%s", response.Code, response.Body.String())
	}
	var lookup protocol.GeoIPLookupResult
	if err := json.Unmarshal(response.Body.Bytes(), &lookup); err != nil {
		t.Fatal(err)
	}
	if len(lookup.Records) != 1 || lookup.Records[0].Address != "1.1.1.1" || lookup.Records[0].ASN != 13335 {
		t.Fatalf("lookup=%#v", lookup)
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/geoip/update", bytes.NewBufferString(`{"operation_id":"geoip-1","if_revision":8}`)))
	if response.Code != http.StatusOK || fake.operation.ID != "geoip-1" || fake.operation.IfRevision == nil || *fake.operation.IfRevision != 8 {
		t.Fatalf("update status=%d operation=%#v body=%s", response.Code, fake.operation, response.Body.String())
	}
}

func TestGeoIPLookupRejectsMalformedDuplicateAndOversizedBatches(t *testing.T) {
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: &fakeRuntime{}})
	addresses := make([]string, 17)
	for index := range addresses {
		addresses[index] = "1.1.1." + string(rune('1'+index))
	}
	oversized, _ := json.Marshal(protocol.GeoIPLookupRequest{Addresses: addresses})
	for _, body := range []string{
		`{"addresses":["not-an-ip"]}`,
		`{"addresses":["127.0.0.1"]}`,
		`{"addresses":["1.1.1.1","1.1.1.1"]}`,
		string(oversized),
	} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/geoip/lookup", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
}
