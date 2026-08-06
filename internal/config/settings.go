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
}

func Defaults() Settings {
	return Settings{
		Schema:         "mihari.settings/v1",
		MixedAddr:      "127.0.0.1:9190",
		ControllerAddr: "127.0.0.1:9090",
		WebAddr:        "127.0.0.1:9191",
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
	settings, err := loadSettings(path)
	if err == nil {
		return settings, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Settings{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Settings{}, false, fmt.Errorf("create settings directory: %w", err)
	}
	lock, err := acquireCreationLock(path + ".lock")
	if err != nil {
		return Settings{}, false, err
	}
	lockPath := lock.Name()
	defer func() {
		_ = lock.Close()
		_ = os.Remove(lockPath)
	}()
	settings, err = loadSettings(path)
	if err == nil {
		return settings, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Settings{}, false, err
	}
	settings = Defaults()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return Settings{}, false, fmt.Errorf("generate controller secret: %w", err)
	}
	settings.ControllerSecret = hex.EncodeToString(secret[:])
	if err := Save(path, settings); err != nil {
		return Settings{}, false, err
	}
	return settings, true, nil
}

// loadSettings loads settings, retrying while the file exists on disk but is
// momentarily unopenable. A concurrent writer replaces it via MoveFileEx;
// during that window Windows reports a sharing violation that is neither
// os.ErrNotExist nor os.ErrPermission, so existence is the reliable retry
// signal. Genuine errors (permissions, deletion) surface immediately.
func loadSettings(path string) (Settings, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		settings, err := Load(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return settings, err
		}
		if _, statErr := os.Stat(path); statErr != nil || time.Now().After(deadline) {
			return Settings{}, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func acquireCreationLock(path string) (*os.File, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return file, nil
		}
		// Windows can report a sharing violation as os.ErrPermission while the
		// winning process still has the lock file open. Treat that transient
		// state exactly like os.ErrExist and retry within the same deadline.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			if _, statError := os.Stat(path); statError != nil {
				return nil, fmt.Errorf("create settings lock: %w", err)
			}
		}
		if time.Now().After(deadline) {
			return nil, dataError("timed out waiting for settings initialization")
		}
		time.Sleep(10 * time.Millisecond)
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
	return nil
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
