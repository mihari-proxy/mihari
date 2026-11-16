package subscription

import (
	"go.yaml.in/yaml/v3"
	"testing"
)

func TestGenerate_AllowLANIsManagedBySettings(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		settings := testSettings()
		settings.AllowLAN = enabled
		if enabled {
			settings.MixedAddr = "0.0.0.0:9190"
		}
		raw, err := Generate(Document{"allow-lan": !enabled}, map[string]any{"allow-lan": !enabled}, settings)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		if document["allow-lan"] != enabled {
			t.Fatalf("allow-lan=%v want %v", document["allow-lan"], enabled)
		}
		if enabled && document["bind-address"] != "0.0.0.0" {
			t.Fatalf("bind-address=%v", document["bind-address"])
		}
	}
}
