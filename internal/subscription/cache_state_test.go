package subscription

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestSubscriptionCacheState_LegacyCatalogWithActiveID(t *testing.T) {
	const active = "0123456789abcdef0123456789abcdef"
	document := "schema: mihari.subscriptions/v1\nglobal-interval: 12h\nactive-id: " + active + "\nprofiles:\n" +
		"- {id: '" + active + "', name: primary, url: 'https://primary.test/sub', enabled: true, generation: 1}\n" +
		"- {id: 'fedcba9876543210fedcba9876543210', name: secondary, url: 'https://secondary.test/sub', enabled: false, generation: 2}\n" +
		"- {id: '00112233445566778899aabbccddeeff', name: missing, url: 'https://missing.test/sub', enabled: true}\n"
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ActiveID != active {
		t.Fatal("active subscription changed")
	}
	encoded, err := yaml.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(encoded), "cache-url:") != 2 {
		t.Fatal("cached profiles did not receive their legacy source")
	}
	if err := Save(path, catalog); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := yaml.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(second) {
		t.Fatal("catalog defaults were not stable")
	}
}

func TestSubscriptionCacheState_PersistAndProject(t *testing.T) {
	document := `schema: mihari.subscriptions/v1
global-interval: 12h
profiles:
- id: "0123456789abcdef0123456789abcdef"
  name: primary
  url: https://new.test/sub
  cache-url: https://old.test/sub
  enabled: true
  generation: 1
  schedule-from: 2026-09-14T01:00:00Z
  interval-refresh-required: true
`
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(path)
	if err != nil {
		t.Fatalf("new catalog could not be loaded: %v", err)
	}
	encoded, err := json.Marshal(catalog.Public())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"cache_outdated":true`, `"interval_refresh_required":true`, `"schedule_from":"2026-09-14T01:00:00Z"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("public state missing %s", field)
		}
	}
	if strings.Contains(string(encoded), ".test") {
		t.Fatal("public state exposed a source URL")
	}
	if err := Save(path, catalog); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(loaded.Public())
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(second) {
		t.Fatal("public cache state changed on reopen")
	}
}
