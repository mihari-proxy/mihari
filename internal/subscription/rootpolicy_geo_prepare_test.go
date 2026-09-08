package subscription

import (
	"context"
	"os"
	"testing"
)

func TestRootPolicy_GeoPreparationDiscoveryAndCompleteBytes(t *testing.T) {
	root, err := decodeProviderTestValue(t, "rules: ['GEOIP,CN,DIRECT', 'GEOSITE,CN@missing,DIRECT', 'IP-ASN,13335,DIRECT']", rootSchema())
	if err != nil {
		t.Fatal(err)
	}
	graph, err := collectPolicyResourceGraph(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := rootPolicyInput()
	wanted, output, err := preparePolicyGeo(context.Background(), input, root, graph, false)
	if err != nil || len(wanted) != 3 || len(output) != 0 {
		t.Fatalf("Geo discovery: %v", err)
	}
	if _, _, err := preparePolicyGeo(context.Background(), input, root, graph, true); err == nil {
		t.Fatal("complete candidate accepted missing Geo")
	}
	input.Resources = make(map[string][]byte)
	for _, fixture := range []struct {
		kind GeoResourceKind
		file string
	}{{GeoCountryMMDB, "country.mmdb"}, {GeoASNMMDB, "asn.mmdb"}} {
		data, err := os.ReadFile("testdata/rootpolicy/mmdb/" + fixture.file)
		if err != nil {
			t.Fatal(err)
		}
		id, err := GeoResourceID(fixture.kind)
		if err != nil {
			t.Fatal(err)
		}
		input.Resources[id] = data
	}
	siteID, err := GeoResourceID(GeoSiteDAT)
	if err != nil {
		t.Fatal(err)
	}
	input.Resources[siteID] = geoSiteFixture()
	wanted, output, err = preparePolicyGeo(context.Background(), input, root, graph, true)
	if err != nil || len(output) != 3 || len(wanted) != 3 {
		t.Fatalf("complete Geo resources: %v", err)
	}
	for _, resource := range output {
		if len(resource.Bytes) == 0 || resource.SHA256 == "" {
			t.Fatal("Geo bytes/hash missing")
		}
	}
	input.Resources[siteID] = []byte("corrupt")
	if _, _, err := preparePolicyGeo(context.Background(), input, root, graph, false); err == nil {
		t.Fatal("discovery ignored corrupt supplied Geo")
	}
}

func TestRootPolicy_GeoPreparationKindsSelectorsAndLoader(t *testing.T) {
	for _, tc := range []struct {
		name, raw      string
		corrupt, valid bool
	}{
		{"DAT actual selector", "geodata-mode: true\nrules: ['GEOIP,CN,DIRECT']", false, true},
		{"missing category", "geodata-mode: true\nrules: ['GEOIP,missing,DIRECT']", false, false},
		{"missing GeoSite category", "rules: ['GEOSITE,missing,DIRECT']", false, false},
		{"wrong loader active", "geodata-loader: unknown\nrules: ['GEOSITE,CN,DIRECT']", false, false},
		{"loader alias", "geodata-loader: memc\nrules: ['GEOSITE,CN,DIRECT']", false, true},
		{"loader standard", "geodata-loader: standard\nrules: ['GEOSITE,CN,DIRECT']", false, true},
		{"unused corrupt asset", "{}", true, false},
		{"unused valid asset", "geodata-loader: unknown", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := decodeProviderTestValue(t, tc.raw, rootSchema())
			if err != nil {
				t.Fatal(err)
			}
			graph, err := collectPolicyResourceGraph(context.Background(), root, nil)
			if err != nil {
				t.Fatal(err)
			}
			input := rootPolicyInput()
			input.Resources = make(map[string][]byte)
			geoIP := encodePolicyWire([]policyWireValue{geoTestMessage(1, geoTestText(1, "CN"), geoTestMessage(2, policyWireValue{number: 1, kind: wireBytes, bytes: []byte{192, 0, 2, 0}}, geoTestUint(2, 24)))})
			ipID, err := GeoResourceID(GeoIPDAT)
			if err != nil {
				t.Fatal(err)
			}
			input.Resources[ipID] = geoIP
			siteID, err := GeoResourceID(GeoSiteDAT)
			if err != nil {
				t.Fatal(err)
			}
			input.Resources[siteID] = geoSiteFixture()
			if tc.corrupt {
				input.Resources[siteID] = []byte("bad")
			}
			_, _, err = preparePolicyGeo(context.Background(), input, root, graph, true)
			if (err == nil) != tc.valid {
				t.Fatalf("Geo prepared validity: %v", err)
			}
		})
	}
	input := rootPolicyInput()
	input.Resources = map[string][]byte{"mihari.geo/v1/geosite-dat": geoSiteFixture()}
	if _, _, err := preparePolicyGeo(context.Background(), input, policyValue{kind: policyObject}, policyResourceGraph{}, false); err == nil {
		t.Fatal("unregistered Geo resource ID accepted")
	}
}

func TestRootPolicy_GeoBudgetSeparateFromProviderWithoutAllocation(t *testing.T) {
	var budget policyGeoBudget
	for i := 0; i < 4; i++ {
		if err := budget.add(128 << 20); err != nil {
			t.Fatal("four fixed128MiB assets rejected")
		}
	}
	if err := budget.add(0); err == nil {
		t.Fatal("fifth Geo resource accepted")
	}
	for _, size := range []int64{-1, (128 << 20) + 1} {
		var invalid policyGeoBudget
		if err := invalid.add(size); err == nil {
			t.Fatal("Geo byte boundary bypassed")
		}
	}
}
