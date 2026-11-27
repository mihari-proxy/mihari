package subscription

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

func TestGenerateOrderedConfigFragments(t *testing.T) {
	settings := testSettings()
	if err := yaml.Unmarshal([]byte(`config-overrides:
  fragments:
    - id: company
      name: Company
      enabled: true
      yaml: |
        dns:
          nameserver-: [B]
          +nameserver: [B]
          nameserver-policy:
            "<+.lab.example>": [local]
`), &settings); err != nil {
		t.Fatal(err)
	}
	base := Document{"dns": map[string]any{"nameserver": []any{"A", "B"}}}
	content, err := Generate(base, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	var generated map[string]any
	if err := yaml.Unmarshal(content, &generated); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"nameserver": []any{"B", "A"}, "nameserver-policy": map[string]any{"+.lab.example": []any{"local"}}}
	if !reflect.DeepEqual(generated["dns"], want) {
		t.Fatalf("effective DNS = %#v, want %#v", generated["dns"], want)
	}
	if !reflect.DeepEqual(base["dns"], map[string]any{"nameserver": []any{"A", "B"}}) {
		t.Fatalf("source subscription was modified: %#v", base)
	}
}

func TestGeneratedSourcesTrackEffectiveAuthUsers(t *testing.T) {
	settings := testSettings()
	settings.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{
		{ID: "one", Name: "Company", Enabled: true, YAML: "authentication+: [alice:fragment, bob:fragment]"},
	}})
	settings.SetProxyAuth(protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "required", Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "form"}}})
	base := Document{"authentication": []any{"invalid", "orig:source"}}
	_, sources, err := GenerateWithSources(base, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"authentication[0]": "", "authentication[1]": "Authentication form", "authentication[2]": "Company", "external-controller": "Mihari", "skip-auth-prefixes": "Authentication form"} {
		if sources[path] != want {
			t.Errorf("%s source %q; want %q", path, sources[path], want)
		}
	}
}

func TestFragmentListRemovalUsesStructuralValueEquality(t *testing.T) {
	settings := testSettings()
	settings.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "drop", Name: "Remove", Enabled: true, YAML: "items-: [{b: 2, a: 1}]"}}})
	base := Document{"items": []any{map[string]any{"a": 1, "b": 2}, "keep", map[string]any{"b": 2, "a": 1}}}
	content, err := Generate(base, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := yaml.Unmarshal(content, &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result["items"], []any{"keep"}) {
		t.Fatalf("items=%#v", result["items"])
	}
}

func TestMissingDeletionDoesNotMaterializeParents(t *testing.T) {
	settings := testSettings()
	settings.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "delete", Name: "Remove", Enabled: true, YAML: "unused: {mapping: {x-: null, y-: []}}"}}})
	content, err := Generate(Document{}, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(content, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["unused"]; ok {
		t.Fatalf("removing missing keys synthesized unused = %#v", out["unused"])
	}
}

func TestInvalidFragmentRetainsYAMLDiagnosticAndIdentity(t *testing.T) {
	settings := testSettings()
	settings.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "dup", Name: "Corporate", Enabled: true, YAML: "dns: {}\ndns: {}"}}})
	_, err := Generate(Document{}, nil, settings)
	var duplicate *yaml.TypeError
	if !errors.As(err, &duplicate) || !strings.Contains(err.Error(), "Corporate") {
		t.Fatalf("missing original YAML error or fragment name: %v", err)
	}
}

func TestConfigOverridesDNSValidationUsesEffectiveSettings(t *testing.T) {
	enabled := true
	for _, tc := range []struct {
		name, fragment string
		form           map[string]protocol.DNSFieldOverride
		valid          bool
	}{
		{"combine inherited resolver with form", "dns: {proxy-server-nameserver: [1.1.1.1]}", map[string]protocol.DNSFieldOverride{"respect-rules": {Operation: "set", Bool: &enabled}}, true},
		{"remove invalid inherited bootstrap", "dns: {default-nameserver: [dns.example, system]}", map[string]protocol.DNSFieldOverride{"default-nameserver": {Operation: "remove", Servers: []string{"dns.example"}}}, true},
		{"replacement repairs invalid inherited bootstrap", "dns: {default-nameserver: [dns.example]}", map[string]protocol.DNSFieldOverride{"default-nameserver": {Operation: "set", Servers: []string{"system"}}}, true},
		{"missing resolver", "dns: {}", map[string]protocol.DNSFieldOverride{"respect-rules": {Operation: "set", Bool: &enabled}}, false},
		{"omitted nameserver inherits core defaults", "dns: {enable: true}", nil, true},
		{"empty nameserver", "dns: {enable: true, nameserver: []}", nil, false},
		{"bootstrap DNS uses concrete IP", "dns: {default-nameserver: [https://dns.example/query]}", nil, false},
		{"bootstrap allows system and encrypted IP", "dns: {default-nameserver: [system, 'https://[2001:db8::1]/query']}", nil, true},
		{"native system URI with authority", "dns: {default-nameserver: [system://resolver.example]}", nil, true},
		{"incomplete DHCP reference", "dns: {default-nameserver: ['dhcp:']}", nil, false},
		{"DHCP missing authority separator", "dns: {default-nameserver: ['dhcp:x']}", nil, false},
		{"legacy DHCP system", "dns: {default-nameserver: [dhcp://system]}", nil, true},
		{"DHCP identifier is not a bootstrap address", "dns: {default-nameserver: [dhcp://192.0.2.1]}", nil, false},
		{"DHCP identifier passes the core IP and port check", "dns: {default-nameserver: [dhcp://192.0.2.1:53]}", nil, true},
		{"invalid mode", "dns: {enhanced-mode: unsupported}", nil, false},
		{"invalid boolean", "dns: {ipv6: banana}", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := testSettings()
			settings.SetOverrides(protocol.ConfigOverrides{DNS: tc.form, Fragments: []protocol.OverrideFragment{{ID: "one", Name: "One", Enabled: true, YAML: tc.fragment}}})
			_, err := Generate(Document{}, nil, settings)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, got error %v", tc.valid, err)
			}
		})
	}
}

func TestFragmentOperationsKeepAbsentEmptyAndNativeValueDistinct(t *testing.T) {
	base := Document{"dns": map[string]any{"nameserver": []any{"main"}, "fallback": []any{"old", "old"}, "nameserver-policy": map[string]any{"+.home": []any{"home"}}}}
	for _, tc := range []struct {
		name, body string
		want       map[string]any
	}{
		{"remove all matches leaves empty field", "dns: {fallback-: [old]}", map[string]any{"nameserver": []any{"main"}, "fallback": []any{}, "nameserver-policy": map[string]any{"+.home": []any{"home"}}}},
		{"empty remove leaves original", "dns: {fallback-: []}", map[string]any{"nameserver": []any{"main"}, "fallback": []any{"old", "old"}, "nameserver-policy": map[string]any{"+.home": []any{"home"}}}},
		{"null removes field", "dns: {fallback-: null}", map[string]any{"nameserver": []any{"main"}, "nameserver-policy": map[string]any{"+.home": []any{"home"}}}},
		{"force map retains literal data keys", "dns!: {nameserver-policy: {'+.lab': lab}, fallback: []}", map[string]any{"nameserver-policy": map[string]any{"+.lab": "lab"}, "fallback": []any{}}},
		{"literal operation left to right", "dns: {nameserver-policy: {'<+.home>+': [next], '+<+.home>': [first]}}", map[string]any{"nameserver": []any{"main"}, "fallback": []any{"old", "old"}, "nameserver-policy": map[string]any{"+.home": []any{"first", "home", "next"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := testSettings()
			settings.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "one", Name: "One", Enabled: true, YAML: tc.body}}})
			rendered, err := Generate(base, nil, settings)
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]any
			if err := yaml.Unmarshal(rendered, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual["dns"], tc.want) {
				t.Fatalf("dns=%#v; want %#v", actual["dns"], tc.want)
			}
		})
	}
}

func TestFragmentProtectionOnlyAppliesToEffectiveTopLevelTargets(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{"'<external-controller>!': forbidden", false}, {"'<secret>-': null", false},
		{"external-ui-url: remote", false}, {"mode: direct", false},
		{"metadata: {'<secret>': allowed}", true}, {"tun: {enable: true, mtu: 1400}", true},
		{"listeners: []", true}, {"external-controller-unix: /fixture/control.sock", true},
		{"rules+: bare", false}, {"rules+: [a]\n---\ndns: {}", false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			settings := testSettings()
			settings.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "one", Name: "One", Enabled: true, YAML: tc.body}}})
			_, err := Generate(Document{}, nil, settings)
			if (err == nil) != tc.valid {
				t.Fatalf("error=%v, want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestDNSFormOverridesFragmentsAndUsesFinalEgress(t *testing.T) {
	settings := testSettings()
	settings.EgressInterface = "eth0"
	settings.SetOverrides(protocol.ConfigOverrides{
		Fragments: []protocol.OverrideFragment{
			{ID: "one", Name: "One", Enabled: true, YAML: "dns: {nameserver+: [two],fallback: [old], ipv6: false}"},
			{ID: "off", Name: "Disabled", Enabled: false, YAML: "["},
			{ID: "two", Name: "Two", Enabled: true, YAML: "dns: {nameserver+: [three]}"},
		},
		DNS: map[string]protocol.DNSFieldOverride{
			"nameserver": {Operation: "append", Servers: []string{"https://dns.invalid/query#previous"}},
			"fallback":   {Operation: "delete"}, "ipv6": {Operation: "set", Bool: new(true)},
		},
	})
	content, sources, err := GenerateWithSources(Document{"dns": map[string]any{"nameserver": []any{"one"}}}, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := yaml.Unmarshal(content, &actual); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"nameserver": []any{"one", "two", "three", "https://dns.invalid/query#eth0"}, "ipv6": true}
	if !reflect.DeepEqual(actual["dns"], want) {
		t.Fatalf("dns=%#v", actual["dns"])
	}
	for path, want := range map[string]string{"dns.nameserver[0]": "", "dns.nameserver[1]": "One", "dns.nameserver[2]": "Two", "dns.nameserver[3]": "Mihari", "dns.ipv6": "DNS form"} {
		if sources[path] != want {
			t.Errorf("%s source=%s, want %s", path, sources[path], want)
		}
	}
}

func TestYAMLMergeKeysRetainOrderedOperations(t *testing.T) {
	fragment := protocol.OverrideFragment{ID: "default", Name: "Default", Enabled: true, YAML: `template!: &defaults
  entries-: [B]
  +entries: [B]
instance:
  <<: *defaults
  entries+: [C]
`}
	settings := testSettings()
	settings.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{fragment}})
	out, err := Generate(Document{"instance": map[string]any{"entries": []any{"A", "B"}}}, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := yaml.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result["instance"], map[string]any{"entries": []any{"B", "A", "C"}}) {
		t.Fatalf("got %#v", result["instance"])
	}
}

func TestEmptyListAdditionLeavesContentsAndSourcesUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides protocol.ConfigOverrides
	}{
		{"form append", protocol.ConfigOverrides{DNS: map[string]protocol.DNSFieldOverride{"default-nameserver": {Operation: "append"}}}},
		{"YAML append", protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "one", Name: "One", Enabled: true, YAML: "dns: {default-nameserver+: []}"}}}},
		{"YAML prepend", protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "one", Name: "One", Enabled: true, YAML: "dns: {+default-nameserver: []}"}}}},
	} {
		for _, base := range []struct {
			name string
			doc  Document
		}{
			{"absent DNS", Document{}},
			{"absent list", Document{"dns": map[string]any{"enable": true}}},
			{"existing list", Document{"dns": map[string]any{"default-nameserver": []any{"system"}}}},
		} {
			t.Run(tc.name+"/"+base.name, func(t *testing.T) {
				settings := testSettings()
				before, beforeSources, err := GenerateWithSources(base.doc, nil, settings)
				if err != nil {
					t.Fatal(err)
				}
				settings.SetOverrides(tc.overrides)
				after, afterSources, err := GenerateWithSources(base.doc, nil, settings)
				if err != nil {
					t.Fatal(err)
				}
				if string(before) != string(after) || !reflect.DeepEqual(beforeSources, afterSources) {
					t.Fatalf("empty list changed effective YAML or source:\n%s\n%v", after, afterSources)
				}
			})
		}
	}
}

func TestEmptyListOverrideRequiresListTarget(t *testing.T) {
	for _, key := range []string{"hosts+", "+hosts", "hosts-"} {
		s := testSettings()
		s.SetOverrides(protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "office", Name: "Office", Enabled: true, YAML: key + ": []"}}})
		if _, err := Generate(Document{"hosts": map[string]any{"example.invalid": "127.0.0.1"}}, nil, s); err == nil || !strings.Contains(err.Error(), "hosts") {
			t.Fatalf("%s accepted a non-list: %v", key, err)
		}
	}
	for _, op := range []string{"append", "remove"} {
		s := testSettings()
		s.SetOverrides(protocol.ConfigOverrides{DNS: map[string]protocol.DNSFieldOverride{"fallback": {Operation: op}}})
		if _, err := Generate(Document{"dns": map[string]any{"fallback": "invalid-shape"}}, nil, s); err == nil || !strings.Contains(err.Error(), "fallback") {
			t.Fatalf("DNS %s accepted a non-list: %v", op, err)
		}
	}
}
