package subscription

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_ProviderTypedOverlayBeforeSemanticValidation(t *testing.T) {
	schema := objectSchema(map[string]*policySchema{
		"routing-mark": integerSchema(0, 9), "udp": boolSchema(),
	})
	schema.required = []string{"udp"}
	schema.memberOverlays = map[string]policyValue{
		"routing-mark": {kind: policyInt, integer: 3},
		"udp":          {kind: policyBool, boolean: true},
	}
	decode := func(raw string) (policyValue, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		return decodePolicyValue(doc.Content[0], schema, "payload[]")
	}
	got, err := decode("routing-mark: 99\n")
	if err != nil {
		t.Fatalf("effective valid override: %v", err)
	}
	mark, _ := got.get("routing-mark")
	udp, _ := got.get("udp")
	if mark.integer != 3 || !udp.boolean {
		t.Fatal("effective fields were not materialized")
	}
	schema.memberOverlays["routing-mark"] = policyValue{kind: policyInt, integer: 8}
	mark, _ = got.get("routing-mark")
	if mark.integer != 3 {
		t.Fatal("output retained mutable overlay")
	}
	for _, raw := range []string{"routing-mark: 1\nrouting-mark: 2\n", "unknown-secret: 1\n"} {
		if _, err := decode(raw); err == nil {
			t.Fatal("source key validation bypassed")
		}
	}
	schema.memberOverlays["routing-mark"] = policyValue{kind: policyInt, integer: 10}
	if _, err := decode("routing-mark: 1\n"); err == nil {
		t.Fatal("invalid effective override accepted")
	}
}
