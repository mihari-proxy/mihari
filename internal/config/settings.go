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

type Settings struct {
	Schema             string         `yaml:"schema"`
	MixedAddr          string         `yaml:"mixed-addr"`
	ControllerAddr     string         `yaml:"controller-addr"`
	WebAddr            string         `yaml:"web-addr"`
	ControllerSecret   string         `yaml:"controller-secret"`
	SystemProxyDesired bool           `yaml:"system-proxy-desired,omitempty"`
	Tun                map[string]any `yaml:"tun,omitempty"` // managed block; empty = unmanaged
	CoreChannel        string         `yaml:"core-channel,omitempty"`
	CoreChannelBundle  string         `yaml:"core-channel-bundle,omitempty"`
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
	return loadOrCreate(path, sidecar)
}

func loadOrCreate(path, sidecar string) (Settings, bool, error) {
	return loadOrCreateWithOps(path, sidecar, defaultSettingsCreationOps())
}

type settingsCreationOps struct {
	now               func() time.Time
	wait              func(time.Duration)
	load              func(string) (Settings, error)
	openLock          func(string) (*os.File, error)
	transientConflict func(error) bool
}

func defaultSettingsCreationOps() settingsCreationOps {
	return settingsCreationOps{
		now:  time.Now,
		wait: time.Sleep,
		load: Load,
		openLock: func(path string) (*os.File, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		},
		transientConflict: isSettingsConflict,
	}
}

func loadOrCreateWithOps(path, sidecar string, ops settingsCreationOps) (Settings, bool, error) {
	deadline := ops.now().Add(10 * time.Second)
	settings, err := ops.load(path)
	if err == nil {
		return persistSidecarIfChanged(path, settings, sidecar)
	}
	if !errors.Is(err, os.ErrNotExist) && !ops.transientConflict(err) {
		return Settings{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Settings{}, false, fmt.Errorf("create settings directory: %w", err)
	}
	settings, lock, err := waitForSettingsOrCreationLock(path, deadline, ops)
	if err != nil {
		return Settings{}, false, err
	}
	if lock == nil {
		return persistSidecarIfChanged(path, settings, sidecar)
	}
	lockPath := lock.Name()
	defer func() {
		_ = lock.Close()
		_ = os.Remove(lockPath)
	}()
	for {
		if !ops.now().Before(deadline) {
			return Settings{}, false, dataError("timed out waiting for settings initialization")
		}
		settings, err = ops.load(path)
		if err == nil {
			return persistSidecarIfChanged(path, settings, sidecar)
		}
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if !ops.transientConflict(err) {
			return Settings{}, false, err
		}
		if !ops.now().Before(deadline) {
			return Settings{}, false, dataError("timed out waiting for settings initialization")
		}
		ops.wait(10 * time.Millisecond)
	}
	settings = Defaults()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return Settings{}, false, fmt.Errorf("generate controller secret: %w", err)
	}
	settings.ControllerSecret = hex.EncodeToString(secret[:])
	if _, err := applySidecarIfPresent(&settings, sidecar); err != nil {
		return Settings{}, false, err
	}
	if err := Save(path, settings); err != nil {
		return Settings{}, false, err
	}
	return settings, true, nil
}

func persistSidecarIfChanged(path string, settings Settings, sidecar string) (Settings, bool, error) {
	changed, err := applySidecarIfPresent(&settings, sidecar)
	if err != nil {
		return Settings{}, false, err
	}
	if changed {
		if err := Save(path, settings); err != nil {
			return Settings{}, false, err
		}
	}
	return settings, false, nil
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
	if err := settings.Validate(); err != nil {
		return err
	}
	if settings.ControllerSecret == "" {
		return dataError("controller secret is required")
	}
	content, err := yaml.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	return AtomicWrite(path, content, 0o600)
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
