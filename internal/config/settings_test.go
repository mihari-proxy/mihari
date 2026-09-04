package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestSettingsLogging_LoadDefaultsAndNormalizesYAML(t *testing.T) {
	// This catches a missing defaulting path for an omitted, partial, or zero-valued log block.
	tests := []struct {
		name string
		log  string
		want LoggingSettings
	}{
		{name: "omitted", want: LoggingSettings{Level: "info", MaxSizeMB: 10, MaxFiles: 3}},
		{name: "empty block", log: "log: {}\n", want: LoggingSettings{Level: "info", MaxSizeMB: 10, MaxFiles: 3}},
		{name: "partial block", log: "log:\n  level: debug\n", want: LoggingSettings{Level: "debug", MaxSizeMB: 10, MaxFiles: 3}},
		{name: "explicit zero limits", log: "log:\n  level: warn\n  max-size-mb: 0\n  max-files: 0\n", want: LoggingSettings{Level: "warn", MaxSizeMB: 10, MaxFiles: 3}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.yaml")
			content := "schema: mihari.settings/v1\nmixed-addr: 127.0.0.1:9190\ncontroller-addr: 127.0.0.1:9090\nweb-addr: 127.0.0.1:9191\ncontroller-secret: " + strings.Repeat("ab", 32) + "\n" + test.log
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			settings, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := settings.EffectiveLogging(); got != test.want {
				t.Fatalf("effective logging=%#v, want %#v", got, test.want)
			}
		})
	}
}

func TestSettingsLogging_LoadAcceptsAllowedLevelsAndRejectsInvalidLimits(t *testing.T) {
	// This catches an incomplete validation range or a level validator that accepts unsupported values.
	for _, level := range []string{"debug", "info", "warn", "error"} {
		t.Run("allows "+level, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.yaml")
			content := "schema: mihari.settings/v1\nmixed-addr: 127.0.0.1:9190\ncontroller-addr: 127.0.0.1:9090\nweb-addr: 127.0.0.1:9191\ncontroller-secret: " + strings.Repeat("ab", 32) + "\nlog:\n  level: " + level + "\n"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err != nil {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}

	for name, log := range map[string]string{
		"level": "  level: trace\n",
		"size":  "  max-size-mb: 101\n",
		"files": "  max-files: 11\n",
	} {
		t.Run("rejects invalid "+name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.yaml")
			content := "schema: mihari.settings/v1\nmixed-addr: 127.0.0.1:9190\ncontroller-addr: 127.0.0.1:9090\nweb-addr: 127.0.0.1:9191\ncontroller-secret: " + strings.Repeat("ab", 32) + "\nlog:\n" + log
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load() error = nil, want invalid logging configuration error")
			}
		})
	}
}

func TestSettingsLogging_SaveCanonicalizesAndOmitsDefaults(t *testing.T) {
	// This catches persistence that serializes a partial non-default block or retains a default log block.
	path := filepath.Join(t.TempDir(), "settings.yaml")
	settings := Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	settings.Logging = &LoggingSettings{Level: "debug"}

	result, err := SaveWithCommit(path, settings)
	if err != nil || !result.Committed || result.Warning != nil {
		t.Fatalf("SaveWithCommit() result=%#v err=%v", result, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"log:", "level: debug", "max-size-mb: 10", "max-files: 3"} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("saved settings missing %q: %s", field, raw)
		}
	}
	if settings.Logging.MaxSizeMB != 0 || settings.Logging.MaxFiles != 0 {
		t.Fatalf("SaveWithCommit mutated settings=%#v", settings)
	}

	settings.SetLogging(LoggingSettings{Level: "debug", MaxSizeMB: 15, MaxFiles: 5})
	if _, err := SaveWithCommit(path, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := loaded.EffectiveLogging(), (LoggingSettings{Level: "debug", MaxSizeMB: 15, MaxFiles: 5}); got != want {
		t.Fatalf("loaded logging=%#v, want %#v", got, want)
	}

	settings.SetLogging(DefaultLoggingSettings())
	if _, err := SaveWithCommit(path, settings); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "log:") {
		t.Fatalf("default logging was serialized: %s", raw)
	}
}

func TestSettingsLogging_LoadOrCreateOutcomeDistinguishesCommitAndNoWrite(t *testing.T) {
	// This catches bootstrap callers being unable to distinguish a pre-commit error from a post-commit warning.
	valid := Defaults()
	valid.ControllerSecret = strings.Repeat("ab", 32)
	warning := errors.New("sync parent directory")

	t.Run("initial creation fails before commit", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.yaml")
		ops := defaultSettingsCreationOps()
		ops.save = func(string, Settings) (CommitResult, error) {
			return CommitResult{}, errors.New("replace failed")
		}
		_, created, result, err := loadOrCreateWithOpsOutcome(path, "", ops)
		if err == nil || created || result.Committed {
			t.Fatalf("created=%v result=%#v err=%v, want pre-commit failure", created, result, err)
		}
	})

	t.Run("initial creation warns after commit", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.yaml")
		ops := defaultSettingsCreationOps()
		ops.save = func(string, Settings) (CommitResult, error) {
			return CommitResult{Committed: true, Warning: warning}, nil
		}
		settings, created, result, err := loadOrCreateWithOpsOutcome(path, "", ops)
		if err != nil || !created || !result.Committed || !errors.Is(result.Warning, warning) {
			t.Fatalf("settings=%#v created=%v result=%#v err=%v", settings, created, result, err)
		}
		if settings.ControllerSecret == "" {
			t.Fatal("created settings must contain a controller secret")
		}
	})

	t.Run("sidecar update fails before commit", func(t *testing.T) {
		root := t.TempDir()
		sidecar := filepath.Join(root, "core-channel")
		if err := os.WriteFile(sidecar, []byte("alpha\nalpha-build\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		ops := defaultSettingsCreationOps()
		ops.load = func(string) (Settings, error) { return valid, nil }
		ops.save = func(string, Settings) (CommitResult, error) {
			return CommitResult{}, errors.New("replace failed")
		}
		_, created, result, err := loadOrCreateWithOpsOutcome(filepath.Join(root, "settings.yaml"), sidecar, ops)
		if err == nil || created || result.Committed {
			t.Fatalf("created=%v result=%#v err=%v, want pre-commit failure", created, result, err)
		}
	})

	t.Run("sidecar update warns after commit", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "settings.yaml")
		sidecar := filepath.Join(root, "core-channel")
		if err := os.WriteFile(sidecar, []byte("alpha\nalpha-build\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		ops := defaultSettingsCreationOps()
		ops.load = func(string) (Settings, error) { return valid, nil }
		ops.save = func(string, Settings) (CommitResult, error) {
			return CommitResult{Committed: true, Warning: warning}, nil
		}
		settings, created, result, err := loadOrCreateWithOpsOutcome(path, sidecar, ops)
		if err != nil || created || !result.Committed || !errors.Is(result.Warning, warning) {
			t.Fatalf("settings=%#v created=%v result=%#v err=%v", settings, created, result, err)
		}
		if settings.CoreChannel != "alpha" || settings.CoreChannelBundle != "alpha-build" {
			t.Fatalf("settings=%#v, want applied sidecar", settings)
		}
	})

	t.Run("existing unchanged settings has no outcome", func(t *testing.T) {
		ops := defaultSettingsCreationOps()
		ops.load = func(string) (Settings, error) { return valid, nil }
		ops.save = func(string, Settings) (CommitResult, error) {
			t.Fatal("SaveWithCommit must not run for unchanged settings")
			return CommitResult{}, nil
		}
		settings, created, result, err := loadOrCreateWithOpsOutcome(filepath.Join(t.TempDir(), "settings.yaml"), "", ops)
		if err != nil || created || result != (CommitResult{}) || !reflect.DeepEqual(settings, valid) {
			t.Fatalf("settings=%#v created=%v result=%#v err=%v", settings, created, result, err)
		}
	})
}

func TestSettingsLogging_CloneDoesNotShareNestedTunOrLogging(t *testing.T) {
	// This catches a candidate mutation leaking through nested YAML values before it is persisted.
	settings := Defaults()
	settings.Tun = map[string]any{
		"dns": map[string]any{
			"nameserver": []any{"a"},
		},
	}
	settings.Logging = &LoggingSettings{Level: "debug", MaxSizeMB: 20, MaxFiles: 4}

	clone := settings.Clone()
	clone.Tun["dns"].(map[string]any)["nameserver"].([]any)[0] = "b"
	clone.Logging.Level = "error"

	if got := settings.Tun["dns"].(map[string]any)["nameserver"].([]any)[0]; got != "a" {
		t.Fatalf("original nested nameserver=%#v, want a", got)
	}
	if settings.Logging.Level != "debug" {
		t.Fatalf("original logging level=%q, want debug", settings.Logging.Level)
	}
}

func TestSettingsCloneDoesNotShareTypedYAMLComposites(t *testing.T) {
	settings := Defaults()
	settings.Tun = map[string]any{
		"nameserver": []string{"https://dns.example/dns-query"},
		"providers": map[string][]string{
			"fallback": {"1.1.1.1"},
		},
		"rules": []map[string][]string{
			{"domains": {"example.com"}},
		},
	}

	clone := settings.Clone()
	clone.Tun["nameserver"].([]string)[0] = "https://changed.example/dns-query"
	clone.Tun["providers"].(map[string][]string)["fallback"][0] = "8.8.8.8"
	clone.Tun["rules"].([]map[string][]string)[0]["domains"][0] = "changed.example"

	if got := settings.Tun["nameserver"].([]string)[0]; got != "https://dns.example/dns-query" {
		t.Fatalf("original typed slice value=%q", got)
	}
	if got := settings.Tun["providers"].(map[string][]string)["fallback"][0]; got != "1.1.1.1" {
		t.Fatalf("original typed map slice value=%q", got)
	}
	if got := settings.Tun["rules"].([]map[string][]string)[0]["domains"][0]; got != "example.com" {
		t.Fatalf("original nested typed composite value=%q", got)
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
	type result struct {
		settings Settings
		created  bool
		err      error
	}
	results := make(chan result, 32)
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			settings, created, err := LoadOrCreateResult(path)
			results <- result{settings: settings, created: created, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	want := ""
	createdCount := 0
	resultCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		resultCount++
		if result.created {
			createdCount++
		}
		if want == "" {
			want = result.settings.ControllerSecret
		}
		if result.settings.ControllerSecret != want {
			t.Fatalf("controller secrets differ: %q and %q", want, result.settings.ControllerSecret)
		}
	}
	if resultCount != 32 || createdCount != 1 || len(want) != 64 {
		t.Fatalf("results=%d created=%d secret length=%d, want 32, 1, 64", resultCount, createdCount, len(want))
	}
	loaded, err := Load(path)
	if err != nil || loaded.ControllerSecret != want {
		t.Fatalf("loaded=%#v err=%v, want persisted secret %q", loaded, err, want)
	}
}

func TestWaitForSettingsOrCreationLock_ReturnsCompletedSettingsDuringConflict(t *testing.T) {
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	now := start
	waits := 0
	want := Settings{
		Schema:           "mihari.settings/v1",
		MixedAddr:        "127.0.0.1:9190",
		ControllerAddr:   "127.0.0.1:9090",
		WebAddr:          "127.0.0.1:9191",
		ControllerSecret: strings.Repeat("ab", 32),
		CoreChannel:      "stable",
	}
	ops := settingsCreationOps{
		now: func() time.Time { return now },
		wait: func(duration time.Duration) {
			waits++
			now = now.Add(duration)
		},
		load:     func(string) (Settings, error) { return want, nil },
		openLock: func(string) (*os.File, error) { return nil, os.ErrExist },
		transientConflict: func(err error) bool {
			return errors.Is(err, os.ErrExist)
		},
	}

	settings, lock, err := waitForSettingsOrCreationLock("settings.yaml", start.Add(10*time.Second), ops)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(settings, want) {
		t.Fatalf("settings=%#v, want %#v", settings, want)
	}
	if lock != nil {
		t.Fatalf("lock=%v, want nil", lock)
	}
	if waits != 0 {
		t.Fatalf("waits=%d, want 0", waits)
	}
}

func TestWaitForSettingsOrCreationLock_RejectsMalformedSettingsImmediately(t *testing.T) {
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	now := start
	waits := 0
	malformed := dataError("invalid settings file")
	ops := settingsCreationOps{
		now: func() time.Time { return now },
		wait: func(duration time.Duration) {
			waits++
			now = now.Add(duration)
		},
		load:     func(string) (Settings, error) { return Settings{}, malformed },
		openLock: func(string) (*os.File, error) { return nil, os.ErrExist },
		transientConflict: func(err error) bool {
			return errors.Is(err, os.ErrExist)
		},
	}

	settings, lock, err := waitForSettingsOrCreationLock("settings.yaml", start.Add(10*time.Second), ops)
	if !reflect.DeepEqual(err, malformed) {
		t.Fatalf("err=%v, want original malformed settings error", err)
	}
	if !reflect.DeepEqual(settings, Settings{}) || lock != nil {
		t.Fatalf("settings=%#v lock=%v, want zero settings and nil lock", settings, lock)
	}
	if waits != 0 {
		t.Fatalf("waits=%d, want 0", waits)
	}
}

func TestWaitForSettingsOrCreationLock_ReturnsTerminalPermissionImmediately(t *testing.T) {
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	waits := 0
	loads := 0
	ops := settingsCreationOps{
		now:  func() time.Time { return start },
		wait: func(time.Duration) { waits++ },
		load: func(string) (Settings, error) {
			loads++
			return Settings{}, os.ErrNotExist
		},
		openLock:          func(string) (*os.File, error) { return nil, fmt.Errorf("open lock: %w", os.ErrPermission) },
		transientConflict: func(error) bool { return false },
	}

	settings, lock, err := waitForSettingsOrCreationLock(filepath.Join(t.TempDir(), "settings.yaml"), start.Add(10*time.Second), ops)
	if !errors.Is(err, os.ErrPermission) || (err != nil && strings.Contains(err.Error(), "timed out")) {
		t.Fatalf("err=%v, want terminal permission error", err)
	}
	if !reflect.DeepEqual(settings, Settings{}) || lock != nil {
		t.Fatalf("settings=%#v lock=%v, want zero settings and nil lock", settings, lock)
	}
	if waits != 0 {
		t.Fatalf("waits=%d, want 0", waits)
	}
	if loads != 1 {
		t.Fatalf("loads=%d, want one immediate settings observation", loads)
	}
}

func TestWaitForSettingsOrCreationLock_ReturnsCompletedSettingsDuringPermissionRace(t *testing.T) {
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	waits := 0
	want := Settings{
		Schema:           "mihari.settings/v1",
		MixedAddr:        "127.0.0.1:9190",
		ControllerAddr:   "127.0.0.1:9090",
		WebAddr:          "127.0.0.1:9191",
		ControllerSecret: strings.Repeat("34", 32),
		CoreChannel:      "stable",
	}
	ops := settingsCreationOps{
		now:      func() time.Time { return start },
		wait:     func(time.Duration) { waits++ },
		load:     func(string) (Settings, error) { return want, nil },
		openLock: func(string) (*os.File, error) { return nil, fmt.Errorf("open lock: %w", os.ErrPermission) },
		transientConflict: func(error) bool {
			return false
		},
	}

	settings, lock, err := waitForSettingsOrCreationLock("settings.yaml", start.Add(10*time.Second), ops)
	if err != nil || lock != nil || !reflect.DeepEqual(settings, want) {
		t.Fatalf("settings=%#v lock=%v err=%v, want completed settings", settings, lock, err)
	}
	if waits != 0 {
		t.Fatalf("waits=%d, want 0", waits)
	}
}

func TestWaitForSettingsOrCreationLock_UsesOneDeadline(t *testing.T) {
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	now := start
	waits := 0
	ops := settingsCreationOps{
		now: func() time.Time { return now },
		wait: func(duration time.Duration) {
			waits++
			now = now.Add(duration)
		},
		load:     func(string) (Settings, error) { return Settings{}, os.ErrNotExist },
		openLock: func(string) (*os.File, error) { return nil, os.ErrExist },
		transientConflict: func(err error) bool {
			return errors.Is(err, os.ErrExist)
		},
	}

	settings, lock, err := waitForSettingsOrCreationLock("settings.yaml", start.Add(10*time.Second), ops)
	if err == nil || err.Error() != "timed out waiting for settings initialization" {
		t.Fatalf("err=%v, want stable initialization timeout", err)
	}
	if !reflect.DeepEqual(settings, Settings{}) || lock != nil {
		t.Fatalf("settings=%#v lock=%v, want zero settings and nil lock", settings, lock)
	}
	if now.Sub(start) > 10*time.Second+10*time.Millisecond {
		t.Fatalf("elapsed=%v, want at most 10.01s", now.Sub(start))
	}
	if waits == 0 {
		t.Fatal("waits=0, want retries until deadline")
	}
}

func TestWaitForSettingsOrCreationLock_ReturnsOnlyAcquiredLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "settings.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = lock.Close()
		_ = os.Remove(lockPath)
	})
	loadCalls := 0
	ops := settingsCreationOps{
		now:      time.Now,
		wait:     time.Sleep,
		load:     func(string) (Settings, error) { loadCalls++; return Settings{}, nil },
		openLock: func(string) (*os.File, error) { return lock, nil },
		transientConflict: func(error) bool {
			return false
		},
	}

	settings, acquired, err := waitForSettingsOrCreationLock("settings.yaml", time.Now().Add(10*time.Second), ops)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(settings, Settings{}) || acquired != lock {
		t.Fatalf("settings=%#v lock=%v, want zero settings and acquired lock", settings, acquired)
	}
	if loadCalls != 0 {
		t.Fatalf("load calls=%d, want 0", loadCalls)
	}
}

func TestLoadOrCreateWithOps_RetriesTransientInitialRead(t *testing.T) {
	valid := Settings{
		Schema:           "mihari.settings/v1",
		MixedAddr:        "127.0.0.1:9190",
		ControllerAddr:   "127.0.0.1:9090",
		WebAddr:          "127.0.0.1:9191",
		ControllerSecret: strings.Repeat("cd", 32),
		CoreChannel:      "stable",
	}
	transient := errors.New("transient settings conflict")

	t.Run("transient conflict", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.yaml")
		if err := os.WriteFile(path, []byte("present"), 0o600); err != nil {
			t.Fatal(err)
		}
		start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
		now := start
		loads := 0
		openCalls := 0
		waits := 0
		ops := settingsCreationOps{
			now: func() time.Time { return now },
			wait: func(duration time.Duration) {
				waits++
				now = now.Add(duration)
			},
			load: func(string) (Settings, error) {
				loads++
				if loads == 1 {
					return Settings{}, transient
				}
				return valid, nil
			},
			openLock: func(string) (*os.File, error) {
				openCalls++
				return nil, os.ErrExist
			},
			transientConflict: func(err error) bool { return errors.Is(err, transient) },
		}

		settings, created, err := loadOrCreateWithOps(path, "", ops)
		if err != nil || created || !reflect.DeepEqual(settings, valid) {
			t.Fatalf("settings=%#v created=%v err=%v", settings, created, err)
		}
		if loads != 2 || openCalls != 1 || waits != 0 {
			t.Fatalf("loads=%d open calls=%d waits=%d, want 2, 1, 0", loads, openCalls, waits)
		}
	})

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "permission", err: fmt.Errorf("read settings: %w", os.ErrPermission)},
		{name: "data", err: dataError("invalid settings file")},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.yaml")
			if err := os.WriteFile(path, []byte("present"), 0o600); err != nil {
				t.Fatal(err)
			}
			start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
			now := start
			waits := 0
			ops := settingsCreationOps{
				now: func() time.Time { return now },
				wait: func(duration time.Duration) {
					waits++
					now = now.Add(duration)
				},
				load:              func(string) (Settings, error) { return Settings{}, test.err },
				openLock:          func(string) (*os.File, error) { return nil, errors.New("unexpected lock attempt") },
				transientConflict: func(error) bool { return false },
			}

			_, _, err := loadOrCreateWithOps(path, "", ops)
			if test.name == "permission" && !errors.Is(err, os.ErrPermission) {
				t.Fatalf("err=%v, want permission error", err)
			}
			if test.name == "data" && !reflect.DeepEqual(err, test.err) {
				t.Fatalf("err=%v, want original data error", err)
			}
			if waits != 0 {
				t.Fatalf("waits=%d, want terminal initial read to fail immediately", waits)
			}
		})
	}
}

func TestWaitForSettingsOrCreationLock_RetriesTransientObservedRead(t *testing.T) {
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	now := start
	waits := 0
	loads := 0
	want := Settings{
		Schema:           "mihari.settings/v1",
		MixedAddr:        "127.0.0.1:9190",
		ControllerAddr:   "127.0.0.1:9090",
		WebAddr:          "127.0.0.1:9191",
		ControllerSecret: strings.Repeat("ef", 32),
		CoreChannel:      "stable",
	}
	transient := errors.New("transient observed read")
	ops := settingsCreationOps{
		now: func() time.Time { return now },
		wait: func(duration time.Duration) {
			waits++
			now = now.Add(duration)
		},
		load: func(string) (Settings, error) {
			loads++
			if loads == 1 {
				return Settings{}, transient
			}
			return want, nil
		},
		openLock: func(string) (*os.File, error) { return nil, os.ErrExist },
		transientConflict: func(err error) bool {
			return errors.Is(err, transient)
		},
	}

	settings, lock, err := waitForSettingsOrCreationLock("settings.yaml", start.Add(10*time.Second), ops)
	if err != nil || lock != nil || !reflect.DeepEqual(settings, want) {
		t.Fatalf("settings=%#v lock=%v err=%v", settings, lock, err)
	}
	if loads != 2 || waits != 1 {
		t.Fatalf("loads=%d waits=%d, want 2 loads and 1 wait", loads, waits)
	}
}

func TestLoadOrCreateWithOps_WaiterPersistsSidecarWithoutCreating(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mihari.yaml")
	sidecar := filepath.Join(root, "core-channel")
	if err := os.WriteFile(sidecar, []byte("alpha\nalpha-bundle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stable := Settings{
		Schema:            "mihari.settings/v1",
		MixedAddr:         "127.0.0.1:9190",
		ControllerAddr:    "127.0.0.1:9090",
		WebAddr:           "127.0.0.1:9191",
		ControllerSecret:  strings.Repeat("12", 32),
		CoreChannel:       "stable",
		CoreChannelBundle: "stable-bundle",
	}
	conflictObserved := make(chan struct{}, 1)
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	now := start
	loads := 0
	ops := settingsCreationOps{
		now: func() time.Time { return now },
		wait: func(duration time.Duration) {
			now = now.Add(duration)
		},
		load: func(string) (Settings, error) {
			loads++
			if loads == 1 {
				return Settings{}, os.ErrNotExist
			}
			select {
			case <-conflictObserved:
				return stable, nil
			default:
				t.Fatal("settings observed before lock conflict")
				return Settings{}, errors.New("settings observed before lock conflict")
			}
		},
		openLock: func(string) (*os.File, error) {
			conflictObserved <- struct{}{}
			return nil, os.ErrExist
		},
		transientConflict: func(error) bool { return false },
	}

	settings, created, err := loadOrCreateWithOps(path, sidecar, ops)
	if err != nil || created {
		t.Fatalf("created=%v err=%v settings=%#v", created, err, settings)
	}
	if settings.CoreChannel != "alpha" || settings.CoreChannelBundle != "alpha-bundle" {
		t.Fatalf("settings=%#v, want persisted alpha sidecar", settings)
	}
	loaded, err := Load(path)
	if err != nil || loaded.CoreChannel != "alpha" || loaded.CoreChannelBundle != "alpha-bundle" || loaded.ControllerSecret != stable.ControllerSecret {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestLoadOrCreateWithOps_CreatorClosesAndRemovesLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari.yaml")
	settings, created, err := loadOrCreateWithOps(path, "", defaultSettingsCreationOps())
	if err != nil || !created || settings.ControllerSecret == "" {
		t.Fatalf("settings=%#v created=%v err=%v", settings, created, err)
	}
	lockPath := path + ".lock"
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock stat err=%v, want not exist", err)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("reopen released lock: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove reopened lock: %v", err)
	}
}

func TestLoadOrCreateWithOps_SharesDeadlineAcrossInitialReadAndCoordination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihari.yaml")
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	now := start
	transient := errors.New("transient initial read")
	openCalls := 0
	loadCalls := 0
	ops := settingsCreationOps{
		now: func() time.Time { return now },
		wait: func(duration time.Duration) {
			now = now.Add(duration)
		},
		load: func(string) (Settings, error) {
			loadCalls++
			if openCalls == 0 && now.Sub(start) < 9*time.Second {
				return Settings{}, transient
			}
			return Settings{}, os.ErrNotExist
		},
		openLock: func(string) (*os.File, error) {
			openCalls++
			return nil, os.ErrExist
		},
		transientConflict: func(err error) bool { return errors.Is(err, transient) },
	}

	_, created, err := loadOrCreateWithOps(path, "", ops)
	if err == nil || err.Error() != "timed out waiting for settings initialization" || created {
		t.Fatalf("created=%v err=%v, want initialization timeout", created, err)
	}
	if openCalls == 0 {
		t.Fatalf("open calls=%d loads=%d, want coordination after initial reads", openCalls, loadCalls)
	}
	if elapsed := now.Sub(start); elapsed > 10*time.Second+10*time.Millisecond {
		t.Fatalf("elapsed=%v, want one shared 10s deadline", elapsed)
	}
}

func TestApplyCoreChannelSidecar(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "core-channel")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	settings := Defaults()
	changed, err := ApplyCoreChannelSidecar(&settings, write(t, "alpha\nalpha-e183c58\n"))
	if err != nil || !changed || settings.CoreChannel != "alpha" || settings.CoreChannelBundle != "alpha-e183c58" {
		t.Fatalf("settings=%#v changed=%v err=%v", settings, changed, err)
	}

	changed, err = ApplyCoreChannelSidecar(&settings, write(t, "alpha\nalpha-e183c58\n"))
	if err != nil || changed {
		t.Fatalf("same stamp should be no-op: changed=%v err=%v", changed, err)
	}

	settings.CoreChannel = "stable" // 模拟 TUI 后来切走
	changed, err = ApplyCoreChannelSidecar(&settings, write(t, "alpha\nalpha-e183c58\n"))
	if err != nil || changed || settings.CoreChannel != "stable" {
		t.Fatalf("old stamp must not revert TUI channel: %#v changed=%v", settings, changed)
	}

	changed, err = ApplyCoreChannelSidecar(&settings, write(t, "alpha\nalpha-ffffff\n"))
	if err != nil || !changed || settings.CoreChannel != "alpha" || settings.CoreChannelBundle != "alpha-ffffff" {
		t.Fatalf("new stamp should apply: %#v changed=%v", settings, changed)
	}

	before := settings
	changed, err = ApplyCoreChannelSidecar(&settings, write(t, "nightly\nstamp\n"))
	if err != nil || changed || !reflect.DeepEqual(settings, before) {
		t.Fatalf("invalid sidecar must be ignored: %#v err=%v", settings, err)
	}

	before = settings
	changed, err = ApplyCoreChannelSidecar(&settings, filepath.Join(t.TempDir(), "missing-core-channel"))
	if err != nil || changed || !reflect.DeepEqual(settings, before) {
		t.Fatalf("missing sidecar must be ignored: %#v changed=%v err=%v", settings, changed, err)
	}
}

func TestLoadOrCreateAppliesSidecarBeforeFirstSave(t *testing.T) {
	root := t.TempDir()
	sidecar := filepath.Join(root, "bin", "core-channel")
	if err := os.MkdirAll(filepath.Dir(sidecar), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, []byte("alpha\nalpha-e183c58\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "mihari.yaml")
	settings, created, err := LoadOrCreateWithSidecar(path, sidecar)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v settings=%#v", created, err, settings)
	}
	if settings.CoreChannel != "alpha" || settings.CoreChannelBundle != "alpha-e183c58" {
		t.Fatalf("settings=%#v", settings)
	}
	if settings.ControllerSecret == "" {
		t.Fatal("controller secret must be generated before first save")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "core-channel: alpha") {
		t.Fatalf("first save missing alpha channel: %s", text)
	}
	if !strings.Contains(text, "core-channel-bundle: alpha-e183c58") {
		t.Fatalf("first save missing sidecar stamp: %s", text)
	}
}

func TestLoadOrCreateWithSidecarAppliesNewStampToExistingSettings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mihari.yaml")
	settings := Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	settings.CoreChannel = "stable"
	settings.CoreChannelBundle = "alpha-e183c58"
	if err := Save(path, settings); err != nil {
		t.Fatal(err)
	}

	sidecar := filepath.Join(root, "bin", "core-channel")
	if err := os.MkdirAll(filepath.Dir(sidecar), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, []byte("alpha\nalpha-ffffff\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, created, err := LoadOrCreateWithSidecar(path, sidecar)
	if err != nil || created {
		t.Fatalf("created=%v err=%v loaded=%#v", created, err, loaded)
	}
	if loaded.CoreChannel != "alpha" || loaded.CoreChannelBundle != "alpha-ffffff" {
		t.Fatalf("loaded=%#v", loaded)
	}

	reloaded, err := Load(path)
	if err != nil || reloaded.CoreChannel != "alpha" || reloaded.CoreChannelBundle != "alpha-ffffff" {
		t.Fatalf("reloaded=%#v err=%v", reloaded, err)
	}
}
