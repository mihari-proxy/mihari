package subscription

import (
	"context"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_InspectDiscoversProvidersThenGeoBeforeCompleteBuild(t *testing.T) {
	input := rootPolicyInput()
	input.YAML = []byte("rule-providers:\n  source: {type: http, behavior: classical, url: 'https://example.test/rules'}\nrules: ['RULE-SET,source,DIRECT']\n")
	policy := NewRootConfigPolicy()
	need, err := policy.Inspect(context.Background(), input)
	if err != nil || len(need.Providers) != 1 || len(need.Geo) != 0 {
		t.Fatalf("provider discovery: %v", err)
	}
	if need.Providers[0].Inline != nil {
		t.Fatal("missing provider invented executable content")
	}
	if _, err := policy.Build(context.Background(), input); err == nil {
		t.Fatal("Build accepted incomplete provider")
	}
	input.Resources = map[string][]byte{need.Providers[0].ResourceID: []byte("payload: ['GEOSITE,CN']")}
	need, err = policy.Inspect(context.Background(), input)
	if err != nil || len(need.Geo) != 1 || need.Geo[0] != GeoSiteDAT {
		t.Fatalf("provider Geo closure: %v", err)
	}
	if _, err := policy.Build(context.Background(), input); err == nil {
		t.Fatal("Build accepted incomplete Geo")
	}
	id, err := GeoResourceID(GeoSiteDAT)
	if err != nil {
		t.Fatal(err)
	}
	input.Resources[id] = geoSiteFixture()
	out, err := policy.Build(context.Background(), input)
	if err != nil || len(out.Providers) != 1 || len(out.Geo) != 1 {
		t.Fatalf("complete build: %v", err)
	}
	if strings.Contains(string(out.YAML), "https://example.test") || strings.Contains(string(out.YAML), "type: http") {
		t.Fatal("native provider downloader remained in executable YAML")
	}
	input.Resources[id] = []byte("corrupt")
	if _, err := policy.Inspect(context.Background(), input); err == nil {
		t.Fatal("Inspect ignored supplied bad Geo")
	}
}

func TestRootPolicy_BuildRequiresGeoAndEmitsDeterministicMode(t *testing.T) {
	policy := NewRootConfigPolicy()
	for _, explicit := range []bool{false, true} {
		input := rootPolicyInput()
		input.YAML = []byte("rules: ['GEOIP,CN,DIRECT']\n")
		if explicit {
			input.YAML = append(input.YAML, []byte("geodata-mode: true\n")...)
		}
		if _, err := policy.Build(context.Background(), input); err == nil {
			t.Fatal("Build accepted missing constructor resource")
		}
		need, err := policy.Inspect(context.Background(), input)
		kind := GeoCountryMMDB
		if explicit {
			kind = GeoIPDAT
		}
		if err != nil || len(need.Geo) != 1 || need.Geo[0] != kind {
			t.Fatalf("effective mode resource: %v", err)
		}
	}
	input := rootPolicyInput()
	out, err := policy.Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		Mode *bool `yaml:"geodata-mode"`
	}
	if err := yaml.Unmarshal(out.YAML, &rendered); err != nil {
		t.Fatal(err)
	}
	if rendered.Mode == nil || *rendered.Mode {
		t.Fatal("omission can inherit prior core Geo mode")
	}
	input.YAML = append(input.YAML, []byte("geodata-mode: true\n")...)
	out, err = policy.Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(out.YAML, &rendered); err != nil {
		t.Fatal(err)
	}
	if rendered.Mode == nil || !*rendered.Mode {
		t.Fatal("explicit DAT mode changed")
	}
}

func TestRootPolicy_SharedPreparationChecksEffectiveOSAndIPv6(t *testing.T) {
	for _, tc := range []struct {
		name, body, os string
		valid          bool
	}{
		{"provider UID Linux", "rule-providers: {unused: {type: inline, behavior: classical, payload: ['UID,42']}}", "linux", true},
		{"provider UID Darwin", "rule-providers: {unused: {type: inline, behavior: classical, payload: ['AND,((UID,42))']}}", "darwin", false},
		{"disabled global clears only pool", "ipv6: false\ndns: {enhanced-mode: fake-ip, fake-ip-range: '', fake-ip-range6: '2001:db8::/64'}", "linux", false},
		{"global true retains v6 pool", "ipv6: true\ndns: {enhanced-mode: fake-ip, fake-ip-range: '', fake-ip-range6: '2001:db8::/64'}", "linux", true},
		{"native global default true", "dns: {enhanced-mode: fake-ip, fake-ip-range: '', fake-ip-range6: '2001:db8::/64'}", "linux", true},
		{"default v4 pool remains", "ipv6: false\ndns: {enhanced-mode: fake-ip, fake-ip-range6: '2001:db8::/64'}", "linux", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := rootPolicyInput()
			input.OS = tc.os
			input.YAML = []byte(tc.body)
			_, buildErr := NewRootConfigPolicy().Build(context.Background(), input)
			_, inspectErr := NewRootConfigPolicy().Inspect(context.Background(), input)
			if (buildErr == nil) != tc.valid || (inspectErr == nil) != tc.valid {
				t.Fatalf("shared effective semantics Build=%v Inspect=%v", buildErr, inspectErr)
			}
		})
	}
}
