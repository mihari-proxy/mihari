package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

const maxSettingsSize = 1 << 20

const (
	DefaultLogLevel           = "info"
	DefaultLogMaxSizeMB int64 = 10
	DefaultLogMaxFiles  int64 = 3
)

// LoggingSettings controls the level and retention limits for Mihari file logs.
type LoggingSettings struct {
	Level     string `yaml:"level"`
	MaxSizeMB int64  `yaml:"max-size-mb"`
	MaxFiles  int64  `yaml:"max-files"`
}

type Settings struct {
	Schema             string           `yaml:"schema"`
	MixedAddr          string           `yaml:"mixed-addr"`
	ControllerAddr     string           `yaml:"controller-addr"`
	WebAddr            string           `yaml:"web-addr"`
	ControllerSecret   string           `yaml:"controller-secret"`
	SystemProxyDesired bool             `yaml:"system-proxy-desired,omitempty"`
	Tun                map[string]any   `yaml:"tun,omitempty"` // managed block; empty = unmanaged
	CoreChannel        string           `yaml:"core-channel,omitempty"`
	CoreChannelBundle  string           `yaml:"core-channel-bundle,omitempty"`
	Logging            *LoggingSettings `yaml:"log,omitempty"`
}

// DefaultLoggingSettings returns the logging configuration used when no override is persisted.
func DefaultLoggingSettings() LoggingSettings {
	return LoggingSettings{
		Level:     DefaultLogLevel,
		MaxSizeMB: DefaultLogMaxSizeMB,
		MaxFiles:  DefaultLogMaxFiles,
	}
}

// EffectiveLogging returns logging settings with omitted and zero-valued fields defaulted.
func (s Settings) EffectiveLogging() LoggingSettings {
	effective := DefaultLoggingSettings()
	if s.Logging == nil {
		return effective
	}
	if s.Logging.Level != "" {
		effective.Level = s.Logging.Level
	}
	if s.Logging.MaxSizeMB != 0 {
		effective.MaxSizeMB = s.Logging.MaxSizeMB
	}
	if s.Logging.MaxFiles != 0 {
		effective.MaxFiles = s.Logging.MaxFiles
	}
	return effective
}

// SetLogging stores a complete non-default logging override or removes the default override.
func (s *Settings) SetLogging(logging LoggingSettings) {
	if logging == DefaultLoggingSettings() {
		s.Logging = nil
		return
	}
	copy := logging
	s.Logging = &copy
}

// Clone returns a copy of Settings that does not share mutable YAML values.
func (s Settings) Clone() Settings {
	clone := s
	if s.Logging != nil {
		logging := *s.Logging
		clone.Logging = &logging
	}
	if s.Tun != nil {
		clone.Tun = make(map[string]any, len(s.Tun))
		for key, value := range s.Tun {
			clone.Tun[key] = cloneYAMLValue(value)
		}
	}
	return clone
}

func cloneYAMLValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		clone := make(map[string]any, len(value))
		for key, nested := range value {
			clone[key] = cloneYAMLValue(nested)
		}
		return clone
	case map[any]any:
		clone := make(map[any]any, len(value))
		for key, nested := range value {
			clone[key] = cloneYAMLValue(nested)
		}
		return clone
	case []any:
		clone := make([]any, len(value))
		for index, nested := range value {
			clone[index] = cloneYAMLValue(nested)
		}
		return clone
	default:
		return value
	}
}

func Defaults() Settings {
	return Settings{
		Schema:         "mihari.settings/v1",
		MixedAddr:      "127.0.0.1:9190",
		ControllerAddr: "127.0.0.1:9090",
		WebAddr:        "127.0.0.1:9191",
		CoreChannel:    "stable",
	}
}

func Load(path string) (Settings, error) {
	file, err := os.Open(path)
	if err != nil {
		return Settings{}, err
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil {
		return Settings{}, err
	} else if info.Size() > maxSettingsSize {
		return Settings{}, dataError("settings file is too large")
	}

	decoder := yaml.NewDecoder(io.LimitReader(file, maxSettingsSize+1))
	decoder.KnownFields(true)
	var settings Settings
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, dataError("invalid settings file")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Settings{}, dataError("settings file must contain one document")
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	if settings.ControllerSecret == "" {
		return Settings{}, dataError("controller secret is required")
	}
	return settings, nil
}

func LoadOrCreate(path string) (Settings, error) {
	settings, _, err := LoadOrCreateResult(path)
	return settings, err
}

// LoadOrCreateResult loads settings or creates them and reports whether this call created the file.
func LoadOrCreateResult(path string) (Settings, bool, error) {
	return loadOrCreate(path, "")
}

// LoadOrCreateWithSidecar loads settings or creates them, applying a packaged
// core-channel sidecar. On first create the sidecar is applied before the first
// Save so the file is never written as a stable-only default and then rewritten.
func LoadOrCreateWithSidecar(path, sidecar string) (Settings, bool, error) {
	settings, created, result, err := LoadOrCreateWithSidecarOutcome(path, sidecar)
	if err != nil {
		return Settings{}, false, err
	}
	if result.Warning != nil {
		return settings, created, result.Warning
	}
	return settings, created, nil
}

// LoadOrCreateWithSidecarOutcome loads or creates settings and reports the outcome of any write.
func LoadOrCreateWithSidecarOutcome(path, sidecar string) (settings Settings, created bool, result CommitResult, err error) {
	return loadOrCreateWithOpsOutcome(path, sidecar, defaultSettingsCreationOps())
}

func loadOrCreate(path, sidecar string) (Settings, bool, error) {
	settings, created, result, err := loadOrCreateWithOpsOutcome(path, sidecar, defaultSettingsCreationOps())
	if err != nil {
		return Settings{}, false, err
	}
	if result.Warning != nil {
		return settings, created, result.Warning
	}
	return settings, created, nil
}

type settingsCreationOps struct {
	now               func() time.Time
	wait              func(time.Duration)
	load              func(string) (Settings, error)
	save              func(string, Settings) (CommitResult, error)
	openLock          func(string) (*os.File, error)
	transientConflict func(error) bool
}

func defaultSettingsCreationOps() settingsCreationOps {
	return settingsCreationOps{
		now:  time.Now,
		wait: time.Sleep,
		load: Load,
		save: SaveWithCommit,
		openLock: func(path string) (*os.File, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		},
		transientConflict: isSettingsConflict,
	}
}

func loadOrCreateWithOps(path, sidecar string, ops settingsCreationOps) (Settings, bool, error) {
	settings, created, result, err := loadOrCreateWithOpsOutcome(path, sidecar, ops)
	if err != nil {
		return Settings{}, false, err
	}
	if result.Warning != nil {
		return settings, created, result.Warning
	}
	return settings, created, nil
}

func loadOrCreateWithOpsOutcome(path, sidecar string, ops settingsCreationOps) (Settings, bool, CommitResult, error) {
	if ops.save == nil {
		ops.save = SaveWithCommit
	}
	deadline := ops.now().Add(10 * time.Second)
	settings, err := ops.load(path)
	if err == nil {
		return persistSidecarIfChangedOutcome(path, settings, sidecar, ops.save)
	}
	if !errors.Is(err, os.ErrNotExist) && !ops.transientConflict(err) {
		return Settings{}, false, CommitResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Settings{}, false, CommitResult{}, fmt.Errorf("create settings directory: %w", err)
	}
	settings, lock, err := waitForSettingsOrCreationLock(path, deadline, ops)
	if err != nil {
		return Settings{}, false, CommitResult{}, err
	}
	if lock == nil {
		return persistSidecarIfChangedOutcome(path, settings, sidecar, ops.save)
	}
	lockPath := lock.Name()
	defer func() {
		_ = lock.Close()
		_ = os.Remove(lockPath)
	}()
	for {
		if !ops.now().Before(deadline) {
			return Settings{}, false, CommitResult{}, dataError("timed out waiting for settings initialization")
		}
		settings, err = ops.load(path)
		if err == nil {
			return persistSidecarIfChangedOutcome(path, settings, sidecar, ops.save)
		}
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if !ops.transientConflict(err) {
			return Settings{}, false, CommitResult{}, err
		}
		if !ops.now().Before(deadline) {
			return Settings{}, false, CommitResult{}, dataError("timed out waiting for settings initialization")
		}
		ops.wait(10 * time.Millisecond)
	}
	settings = Defaults()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return Settings{}, false, CommitResult{}, fmt.Errorf("generate controller secret: %w", err)
	}
	settings.ControllerSecret = hex.EncodeToString(secret[:])
	if _, err := applySidecarIfPresent(&settings, sidecar); err != nil {
		return Settings{}, false, CommitResult{}, err
	}
	result, err := ops.save(path, settings)
	if err != nil {
		return Settings{}, false, result, err
	}
	return settings, true, result, nil
}

func persistSidecarIfChanged(path string, settings Settings, sidecar string) (Settings, bool, error) {
	settings, created, result, err := persistSidecarIfChangedOutcome(path, settings, sidecar, SaveWithCommit)
	if err != nil {
		return Settings{}, false, err
	}
	if result.Warning != nil {
		return settings, created, result.Warning
	}
	return settings, created, nil
}

func persistSidecarIfChangedOutcome(path string, settings Settings, sidecar string, save func(string, Settings) (CommitResult, error)) (Settings, bool, CommitResult, error) {
	changed, err := applySidecarIfPresent(&settings, sidecar)
	if err != nil {
		return Settings{}, false, CommitResult{}, err
	}
	if changed {
		result, err := save(path, settings)
		if err != nil {
			return Settings{}, false, result, err
		}
		return settings, false, result, nil
	}
	return settings, false, CommitResult{}, nil
}

func applySidecarIfPresent(settings *Settings, sidecar string) (bool, error) {
	if sidecar == "" {
		return false, nil
	}
	return ApplyCoreChannelSidecar(settings, sidecar)
}

func waitForSettingsOrCreationLock(path string, deadline time.Time, ops settingsCreationOps) (Settings, *os.File, error) {
	for {
		if !ops.now().Before(deadline) {
			return Settings{}, nil, dataError("timed out waiting for settings initialization")
		}
		file, err := ops.openLock(path + ".lock")
		if err == nil {
			return Settings{}, file, nil
		}

		settings, loadErr := ops.load(path)
		if loadErr == nil {
			return settings, nil, nil
		}
		if !errors.Is(err, os.ErrExist) && !ops.transientConflict(err) {
			return Settings{}, nil, fmt.Errorf("create settings lock: %w", err)
		}
		if !errors.Is(loadErr, os.ErrNotExist) && !ops.transientConflict(loadErr) {
			return Settings{}, nil, loadErr
		}
		if !ops.now().Before(deadline) {
			return Settings{}, nil, dataError("timed out waiting for settings initialization")
		}
		ops.wait(10 * time.Millisecond)
	}
}

func Save(path string, settings Settings) error {
	result, err := SaveWithCommit(path, settings)
	if err != nil {
		return err
	}
	return result.Warning
}

// SaveWithCommit validates and canonically persists settings without modifying its argument.
func SaveWithCommit(path string, settings Settings) (CommitResult, error) {
	if err := settings.Validate(); err != nil {
		return CommitResult{}, err
	}
	if settings.ControllerSecret == "" {
		return CommitResult{}, dataError("controller secret is required")
	}
	canonical := settings.Clone()
	canonical.SetLogging(settings.EffectiveLogging())
	content, err := yaml.Marshal(canonical)
	if err != nil {
		return CommitResult{}, fmt.Errorf("encode settings: %w", err)
	}
	return AtomicWriteWithCommit(path, content, 0o600)
}

func (s Settings) Validate() error {
	if s.Schema != "mihari.settings/v1" {
		return dataError("unsupported settings schema")
	}
	mixed, err := parseEndpoint("mixed-addr", s.MixedAddr, false)
	if err != nil {
		return err
	}
	controller, err := parseEndpoint("controller-addr", s.ControllerAddr, true)
	if err != nil {
		return err
	}
	web, err := parseEndpoint("web-addr", s.WebAddr, true)
	if err != nil {
		return err
	}
	if mixed.Port() == controller.Port() || mixed.Port() == web.Port() || controller.Port() == web.Port() {
		return dataError("managed ports must be distinct")
	}
	if s.ControllerSecret != "" {
		decoded, err := hex.DecodeString(s.ControllerSecret)
		if err != nil || len(decoded) != 32 {
			return dataError("invalid controller secret")
		}
	}
	if err := validateTun(s.Tun); err != nil {
		return err
	}
	logging := s.EffectiveLogging()
	switch logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return dataError("invalid log level")
	}
	if logging.MaxSizeMB < 1 || logging.MaxSizeMB > 100 {
		return dataError("log max size must be between 1 and 100 MiB")
	}
	if logging.MaxFiles < 1 || logging.MaxFiles > 10 {
		return dataError("log max files must be between 1 and 10")
	}
	switch s.CoreChannel {
	case "", "stable", "alpha":
	default:
		return dataError("invalid core channel")
	}
	return nil
}

// ApplyCoreChannelSidecar applies a packaged core-channel sidecar to settings.
// A missing or invalid sidecar is ignored. An unchanged stamp is a no-op so a
// later TUI channel switch is not reverted by an old sidecar file.
func ApplyCoreChannelSidecar(settings *Settings, sidecarPath string) (bool, error) {
	raw, err := os.ReadFile(sidecarPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return false, nil
	}
	channel := strings.TrimSpace(lines[0])
	stamp := strings.TrimSpace(lines[1])
	if stamp == "" || (channel != "stable" && channel != "alpha") {
		return false, nil
	}
	if settings.CoreChannelBundle == stamp {
		return false, nil
	}
	settings.CoreChannel = channel
	settings.CoreChannelBundle = stamp
	return true, nil
}

func validateTun(tun map[string]any) error {
	if tun == nil {
		return nil
	}
	if enable, ok := tun["enable"]; ok {
		if _, isBool := enable.(bool); !isBool {
			return dataError("tun.enable must be a boolean")
		}
	}
	if stack, ok := tun["stack"]; ok {
		if _, isString := stack.(string); !isString {
			return dataError("tun.stack must be a string")
		}
	}
	return nil
}

func parseEndpoint(name, value string, loopback bool) (netip.AddrPort, error) {
	endpoint, err := netip.ParseAddrPort(value)
	if err != nil || endpoint.Port() == 0 {
		return netip.AddrPort{}, dataError(name + " must be an IP address and valid port")
	}
	if loopback && !endpoint.Addr().IsLoopback() {
		return netip.AddrPort{}, dataError(name + " must use a loopback address")
	}
	return endpoint, nil
}

func dataError(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}
