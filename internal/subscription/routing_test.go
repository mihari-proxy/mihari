package subscription

import (
	"go.yaml.in/yaml/v3"
	"testing"
)

func TestGenerate_RoutingModeDefaultsToManagedRule(t *testing.T) {
	for _, mode := range []string{"global", "direct"} {
		t.Run(mode, func(t *testing.T) {
			base := Document{"mode": mode}
			content, err := Generate(base, map[string]any{"mode": mode}, testSettings())
			if err != nil {
				t.Fatal(err)
			}
			var got Document
			if err := yaml.Unmarshal(content, &got); err != nil {
				t.Fatal(err)
			}
			if got["mode"] != "rule" {
				t.Fatalf("mode = %v, want managed rule", got["mode"])
			}
			if base["mode"] != mode {
				t.Fatal("source document mutated")
			}
		})
	}
}

func TestGenerate_RoutingModeUsesSavedIntent(t *testing.T) {
	for _, mode := range []string{"rule", "global", "direct"} {
		s := testSettings()
		s.SetRoutingMode(mode)
		content, err := Generate(Document{"mode": "direct"}, map[string]any{"mode": "global"}, s)
		if err != nil {
			t.Fatal(err)
		}
		var got Document
		if err := yaml.Unmarshal(content, &got); err != nil {
			t.Fatal(err)
		}
		if got["mode"] != mode {
			t.Fatalf("mode = %v, want %s", got["mode"], mode)
		}
	}
}
