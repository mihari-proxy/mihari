package subscription

import (
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
	"math"
	"strings"
	"testing"
)

func TestRootPolicy_TypedTreePreservesValuesAndOrder(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("second: [true, false]\nfirst: [false]\n"), &doc); err != nil {
		t.Fatal(err)
	}
	schema := &policySchema{kind: policyObject, dynamic: &policySchema{kind: policyList, element: &policySchema{kind: policyBool}}}
	got, err := decodePolicyValue(doc.Content[0], schema, "headers")
	if err != nil {
		t.Fatal(err)
	}
	if got.kind != policyObject || len(got.fields) != 2 || got.fields[0].name != "second" || got.fields[1].name != "first" || len(got.fields[0].value.items) != 2 || !got.fields[0].value.items[0].boolean || got.fields[0].value.items[1].boolean {
		t.Fatal("typed ordered data lost")
	}
	// Mutating the input syntax cannot change the decoded object or its scalar data.
	doc.Content[0].Content[0].Value = "changed"
	doc.Content[0].Content[1].Content[0].Value = "false"
	if got.fields[0].name != "second" || !got.fields[0].value.items[0].boolean {
		t.Fatal("source syntax retained")
	}
}

func TestRootPolicy_TypedTreeScalarBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		schema    *policySchema
		good, bad string
	}{
		{"string", &policySchema{kind: policyString}, "'network data'", "42"},
		{"bool", &policySchema{kind: policyBool}, "true", "'true'"},
		{"signed", &policySchema{kind: policyInt, min: -2, max: 2}, "-2", "3"},
		{"unsigned", &policySchema{kind: policyUint, maxUint: math.MaxUint64}, "18446744073709551615", "18446744073709551616"},
		{"uint16", &policySchema{kind: policyUint, maxUint: math.MaxUint16}, "65535", "65536"},
		{"finite", &policySchema{kind: policyFloat}, "1.5", ".inf"},
		{"enum", &policySchema{kind: policyString, choices: []string{"rule", "global", "direct"}}, "rule", "secret-value"},
		{"nullable", &policySchema{kind: policyBool, nullable: true}, "null", "'null'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, raw := range []string{tc.good, tc.bad} {
				var doc yaml.Node
				if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
					t.Fatal(err)
				}
				got, err := decodePolicyValue(doc.Content[0], tc.schema, "fixture")
				if i == 0 {
					if err != nil {
						t.Fatalf("positive control: %v", err)
					}
					encoded, err := yaml.Marshal(got.yamlNode())
					if err != nil {
						t.Fatal(err)
					}
					var again yaml.Node
					if err := yaml.Unmarshal(encoded, &again); err != nil {
						t.Fatal(err)
					}
					if _, err := decodePolicyValue(again.Content[0], tc.schema, "fixture"); err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("scalar boundary accepted")
				}
			}
		})
	}
}

func TestRootPolicy_TypedTreeRejectsStructuralAmbiguity(t *testing.T) {
	schema := &policySchema{kind: policyObject, fields: map[string]*policySchema{"name": {kind: policyString}}, normalize: strings.ToLower}
	for _, raw := range []string{
		"name: valid\nname: secret-value\n", "Name: valid\nname: secret-value\n",
		"name: &alias secret-value\n", "name: !custom secret-value\n",
		"name: valid\nsecret-value: secret-value\n", "name: valid\n<<: {name: secret-value}\n",
		"{42: secret-value}", "{name: [secret-value]}",
	} {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		_, err := decodePolicyValue(doc.Content[0], schema, "fixture")
		var failure PolicyError
		if !errors.As(err, &failure) || failure.Code != protocol.CodeDataFailure {
			t.Fatalf("structural boundary accepted: %v", err)
		}
		if strings.Contains(err.Error(), "secret-value") {
			t.Fatal("untrusted value in error")
		}
	}
}

func TestRootPolicy_TypedDiscriminatorClosesBranchFields(t *testing.T) {
	schema := &policySchema{kind: policyObject, discriminator: "type", variants: map[string]*policySchema{
		"network":  {kind: policyObject, required: []string{"type", "port"}, fields: map[string]*policySchema{"type": stringSchema(), "port": unsignedSchema(65535)}},
		"internal": {kind: policyObject, required: []string{"type"}, fields: map[string]*policySchema{"type": stringSchema()}},
	}}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("{type: network, port: 443}"), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := decodePolicyValue(doc.Content[0], schema, "proxy")
	if err != nil {
		t.Fatalf("valid typed branch rejected: %v", err)
	}
	if port, ok := got.get("port"); !ok || port.unsigned != 443 {
		t.Fatal("typed discriminator branch lost")
	}
	for _, bad := range []string{"{type: network}", "{type: internal, port: 443}", "{type: file, path: secret-value}", "{type: [network], port: 443}"} {
		if err := yaml.Unmarshal([]byte(bad), &doc); err != nil {
			t.Fatal(err)
		}
		if _, err := decodePolicyValue(doc.Content[0], schema, "proxy"); err == nil {
			t.Fatal("invalid discriminator branch accepted")
		}
	}
}
