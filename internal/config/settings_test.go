package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestDefaultSettingsUseManagedPortsAndLoopback(t *testing.T) {
	settings := Defaults()
	if settings.Schema != "mihari.settings/v1" || settings.MixedAddr != "127.0.0.1:9190" || settings.ControllerAddr != "127.0.0.1:9090" || settings.WebAddr != "127.0.0.1:9191" {
		t.Fatalf("settings=%#v", settings)
	}
	if settings.SystemProxyDesired {
		t.Fatalf("default system-proxy-desired=%v, want false", settings.SystemProxyDesired)
	}
	if settings.Tun != nil {
		t.Fatalf("default tun=%#v, want nil", settings.Tun)
	}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsValidationRejectsUnsafeOrConflictingAddresses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Settings)
	}{
		{"public controller", func(s *Settings) { s.ControllerAddr = "0.0.0.0:9090" }},
		{"public web", func(s *Settings) { s.WebAddr = "192.0.2.1:9191" }},
		{"duplicate port", func(s *Settings) { s.WebAddr = "127.0.0.1:9090" }},
		{"invalid mixed port", func(s *Settings) { s.MixedAddr = "127.0.0.1:70000" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := Defaults()
			test.mutate(&settings)
			if err := settings.Validate(); err == nil {
				t.Fatalf("expected invalid settings: %#v", settings)
			}
		})
	}
}

func TestLoadOrCreateSettingsIsStablePrivateAndStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "mihari.yaml")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first.ControllerSecret) != 64 {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("mode=%v", info.Mode().Perm())
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "unknown-setting:") {
		t.Fatal("unexpected fixture collision")
	}
	raw = append(raw, []byte("unknown-setting: true\n")...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown YAML field to fail")
	}
}

func TestLoadOrCreateResultReportsOnlyNewlyCreatedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari.yaml")
	first, created, err := LoadOrCreateResult(path)
	if err != nil || !created || first.ControllerSecret == "" {
		t.Fatalf("first=%#v created=%v err=%v", first, created, err)
	}
	second, created, err := LoadOrCreateResult(path)
	if err != nil || created || second.ControllerSecret != first.ControllerSecret {
		t.Fatalf("second=%#v created=%v err=%v", second, created, err)
	}
}

func TestSaveRejectsMissingControllerSecret(t *testing.T) {
	settings := Defaults()
	if err := Save(filepath.Join(t.TempDir(), "mihari.yaml"), settings); err == nil {
		t.Fatal("expected missing controller secret to fail")
	}
}

func TestSettingsPersistSystemProxyAndTunDesired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari.yaml")
	settings := Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	settings.SystemProxyDesired = true
	settings.Tun = map[string]any{
		"enable": true,
		"stack":  "gVisor",
	}
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.SystemProxyDesired {
		t.Fatalf("system-proxy-desired=%v, want true", loaded.SystemProxyDesired)
	}
	if loaded.Tun["enable"] != true {
		t.Fatalf("tun.enable=%#v, want true", loaded.Tun["enable"])
	}
	if loaded.Tun["stack"] != "gVisor" {
		t.Fatalf("tun.stack=%#v, want gVisor", loaded.Tun["stack"])
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "system-proxy-desired: true") {
		t.Fatalf("saved YAML missing system-proxy-desired: %s", text)
	}
	if !strings.Contains(text, "tun:") || !strings.Contains(text, "enable: true") || !strings.Contains(text, "stack: gVisor") {
		t.Fatalf("saved YAML missing tun block: %s", text)
	}
}

func TestLoadSettingsWithoutSystemProxyAndTunIsBackwardCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari.yaml")
	settings := Defaults()
	settings.ControllerSecret = strings.Repeat("cd", 32)
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SystemProxyDesired {
		t.Fatalf("system-proxy-desired=%v, want false", loaded.SystemProxyDesired)
	}
	if loaded.Tun != nil {
		t.Fatalf("tun=%#v, want nil", loaded.Tun)
	}
}

func TestSettingsValidationRejectsInvalidTunTypes(t *testing.T) {
	tests := []struct {
		name string
		tun  map[string]any
	}{
		{"enable string", map[string]any{"enable": "yes"}},
		{"enable int", map[string]any{"enable": 1}},
		{"stack bool", map[string]any{"enable": true, "stack": true}},
		{"stack int", map[string]any{"stack": 42}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := Defaults()
			settings.Tun = test.tun
			if err := settings.Validate(); err == nil {
				t.Fatalf("expected invalid tun settings: %#v", settings)
			}
		})
	}
}

func TestDefaultsCoreChannelIsStable(t *testing.T) {
	settings := Defaults()
	if settings.CoreChannel != "stable" {
		t.Fatalf("CoreChannel=%q", settings.CoreChannel)
	}
	if settings.CoreChannelBundle != "" {
		t.Fatalf("CoreChannelBundle=%q", settings.CoreChannelBundle)
	}
}

func TestSettingsCoreChannelRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari.yaml")
	settings := Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CoreChannel != "stable" {
		t.Fatalf("CoreChannel=%q, want stable from Defaults", loaded.CoreChannel)
	}

	settings.CoreChannel = "alpha"
	settings.CoreChannelBundle = "alpha-e183c58"
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path)
	if err != nil || loaded.CoreChannel != "alpha" || loaded.CoreChannelBundle != "alpha-e183c58" {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}

	settings.CoreChannel = "nightly"
	if err := settings.Validate(); err == nil {
		t.Fatal("expected invalid core-channel")
	}

	omittedPath := filepath.Join(t.TempDir(), "legacy.yaml")
	legacy := strings.Join([]string{
		"schema: mihari.settings/v1",
		"mixed-addr: 127.0.0.1:9190",
		"controller-addr: 127.0.0.1:9090",
		"web-addr: 127.0.0.1:9191",
		"controller-secret: " + strings.Repeat("ab", 32),
		"",
	}, "\n")
	if err := os.WriteFile(omittedPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(omittedPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CoreChannel != "" {
		t.Fatalf("omitted core-channel loaded as %q", loaded.CoreChannel)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsValidationAcceptsValidTunBlock(t *testing.T) {
	settings := Defaults()
	settings.Tun = map[string]any{
		"enable": true,
		"stack":  "system",
	}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	settings.Tun = map[string]any{}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	settings.SystemProxyDesired = true
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentLoadOrCreateUsesOneControllerSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari.yaml")
	start := make(chan struct{})
	results := make(chan Settings, 32)
	errors := make(chan error, 32)
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			settings, err := LoadOrCreate(path)
			results <- settings
			errors <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := ""
	for settings := range results {
		if want == "" {
			want = settings.ControllerSecret
		}
		if settings.ControllerSecret != want {
			t.Fatalf("controller secrets differ: %q and %q", want, settings.ControllerSecret)
		}
	}
}
