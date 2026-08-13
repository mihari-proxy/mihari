package subscription

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCatalogRoundTripAndPublicRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscriptions", "catalog.yaml")
	now := time.Unix(100, 0).UTC()
	catalog := Defaults()
	catalog.Profiles = []Profile{{
		ID: "0123456789abcdef0123456789abcdef", Name: "primary",
		URL: "https://user:pass@example.test/sub?token=secret", Enabled: true,
		AutoRefresh: true, Interval: "30m", Generation: 2, UpdatedAt: now,
	}}
	catalog.ActiveID = catalog.Profiles[0].ID
	if err := Save(path, catalog); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profiles[0].URL != catalog.Profiles[0].URL || loaded.ActiveID != catalog.ActiveID {
		t.Fatalf("round trip mismatch: %#v", loaded)
	}
	encoded, err := json.Marshal(loaded.Public())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "example.test") {
		t.Fatalf("public catalog leaked URL: %s", encoded)
	}
}

func TestLoadRejectsUnknownFieldsAndInvalidInterval(t *testing.T) {
	for name, document := range map[string]string{
		"unknown":  "schema: mihari.subscriptions/v1\nunknown: true\n",
		"interval": "schema: mihari.subscriptions/v1\nglobal-interval: never\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.yaml")
			if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("expected load failure")
			}
		})
	}
}

func TestValidateRepairsActiveAndRejectsDuplicateIDs(t *testing.T) {
	catalog := Defaults()
	catalog.ActiveID = "missing"
	catalog.Profiles = []Profile{
		{ID: "0123456789abcdef0123456789abcdef", Name: "disabled", URL: "https://one.test", Enabled: false, AutoRefresh: true},
		{ID: "fedcba9876543210fedcba9876543210", Name: "enabled", URL: "https://two.test", Enabled: true, AutoRefresh: true, Generation: 1},
	}
	if err := catalog.Normalize(); err != nil {
		t.Fatal(err)
	}
	if catalog.ActiveID != catalog.Profiles[1].ID {
		t.Fatalf("active=%q", catalog.ActiveID)
	}
	catalog.Profiles[1].ID = catalog.Profiles[0].ID
	if err := catalog.Normalize(); err == nil {
		t.Fatal("expected duplicate ID failure")
	}
}

func TestEffectiveInterval(t *testing.T) {
	catalog := Defaults()
	profile := Profile{Interval: "45m"}
	if got := catalog.EffectiveInterval(profile); got != 45*time.Minute {
		t.Fatalf("interval=%v", got)
	}
	profile.Interval = ""
	if got := catalog.EffectiveInterval(profile); got != 12*time.Hour {
		t.Fatalf("global interval=%v", got)
	}
}

func TestPublicCatalogExposesProxyMode(t *testing.T) {
	catalog := Defaults()
	catalog.Profiles = []Profile{
		{ID: "0123456789abcdef0123456789abcdef", Name: "direct", URL: "https://one.test"},
		{ID: "fedcba9876543210fedcba9876543210", Name: "proxy", URL: "https://two.test", ProxyMode: ProxyModeProxy},
		{ID: "0123456789abcdef0123456789abcde0", Name: "auto", URL: "https://three.test", ProxyMode: ProxyModeAuto},
	}
	public := catalog.Public()
	want := []string{ProxyModeDirect, ProxyModeProxy, ProxyModeAuto}
	if len(public.Profiles) != len(want) {
		t.Fatalf("profile count=%d", len(public.Profiles))
	}
	for index, expected := range want {
		if got := public.Profiles[index].ProxyMode; got != expected {
			t.Fatalf("profile[%d] proxy mode=%q want %q", index, got, expected)
		}
	}
}

func TestNormalizeValidatesProxyMode(t *testing.T) {
	for _, mode := range []string{ProxyModeDirect, ProxyModeProxy, ProxyModeAuto} {
		catalog := Defaults()
		catalog.Profiles = []Profile{{ID: "0123456789abcdef0123456789abcdef", Name: "ok", URL: "https://one.test", ProxyMode: mode}}
		if err := catalog.Normalize(); err != nil {
			t.Fatalf("mode=%q should be valid: %v", mode, err)
		}
	}
	catalog := Defaults()
	catalog.Profiles = []Profile{{ID: "0123456789abcdef0123456789abcdef", Name: "bad", URL: "https://one.test", ProxyMode: "bogus"}}
	if err := catalog.Normalize(); err == nil {
		t.Fatal("expected invalid proxy mode to be rejected")
	}
}

// fillDefaults picks the first enabled profile with a fetched generation when
// no active id is set; leaves a set id untouched; no-op without a usable profile.
func TestFillDefaultsSelectsActiveProfile(t *testing.T) {
	disabled := Profile{ID: "0123456789abcdef0123456789abcdef", Name: "disabled", URL: "https://one.test", Enabled: false, AutoRefresh: true}
	enabled := Profile{ID: "fedcba9876543210fedcba9876543210", Name: "enabled", URL: "https://two.test", Enabled: true, AutoRefresh: true, Generation: 1}
	t.Run("picks first enabled with generation", func(t *testing.T) {
		catalog := Defaults()
		catalog.Profiles = []Profile{disabled, enabled}
		catalog.fillDefaults()
		if catalog.ActiveID != enabled.ID {
			t.Fatalf("active=%q want %q", catalog.ActiveID, enabled.ID)
		}
	})
	t.Run("leaves existing id untouched", func(t *testing.T) {
		catalog := Defaults()
		catalog.ActiveID = "preset"
		catalog.Profiles = []Profile{enabled}
		catalog.fillDefaults()
		if catalog.ActiveID != "preset" {
			t.Fatalf("overwrote set id: %q", catalog.ActiveID)
		}
	})
	t.Run("no-op without a usable profile", func(t *testing.T) {
		catalog := Defaults()
		catalog.Profiles = []Profile{disabled}
		catalog.fillDefaults()
		if catalog.ActiveID != "" {
			t.Fatalf("active=%q want empty", catalog.ActiveID)
		}
	})
}

// migrate must not mutate a current-schema catalog (reflective deep check,
// not just a few fields, so future profile-level migrations can't slip through).
func TestMigrateNoOpForCurrentSchema(t *testing.T) {
	catalog := Defaults()
	catalog.Profiles = []Profile{{ID: "0123456789abcdef0123456789abcdef", Name: "ok", URL: "https://one.test", Enabled: true, AutoRefresh: true, Generation: 1}}
	catalog.ActiveID = catalog.Profiles[0].ID
	before := catalog
	catalog.migrate()
	if !reflect.DeepEqual(before, catalog) {
		t.Fatalf("migrate mutated current-schema catalog:\n before=%#v\n after=%#v", before, catalog)
	}
}
