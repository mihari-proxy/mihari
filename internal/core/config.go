package core

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"reflect"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

type bootstrapDocument struct {
	MixedPort          uint16   `yaml:"mixed-port"`
	AllowLAN           bool     `yaml:"allow-lan"`
	BindAddress        string   `yaml:"bind-address"`
	Mode               string   `yaml:"mode"`
	LogLevel           string   `yaml:"log-level"`
	ExternalController string   `yaml:"external-controller"`
	Secret             string   `yaml:"secret"`
	Proxies            []any    `yaml:"proxies"`
	ProxyGroups        []any    `yaml:"proxy-groups"`
	Rules              []string `yaml:"rules"`
}

func BootstrapConfig(settings config.Settings) ([]byte, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if settings.ControllerSecret == "" {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "controller secret is required"}
	}
	mixed, err := netip.ParseAddrPort(settings.MixedAddr)
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid mixed address"}
	}
	document := bootstrapDocument{
		MixedPort:          mixed.Port(),
		AllowLAN:           false,
		BindAddress:        mixed.Addr().String(),
		Mode:               "rule",
		LogLevel:           "info",
		ExternalController: settings.ControllerAddr,
		Secret:             settings.ControllerSecret,
		Proxies:            []any{},
		ProxyGroups:        []any{},
		Rules:              []string{"MATCH,DIRECT"},
	}
	content, err := yaml.Marshal(document)
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "encode bootstrap configuration"}
	}
	return content, nil
}

func WriteBootstrapConfig(path string, settings config.Settings) error {
	content, err := BootstrapConfig(settings)
	if err != nil {
		return err
	}
	return config.AtomicWrite(path, content, 0o600)
}

func EnsureRuntimeConfig(path string, settings config.Settings) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return WriteBootstrapConfig(path, settings)
	}
	if err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "read runtime configuration"}
	}
	if len(content) > 32<<20 {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "runtime configuration is too large"}
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid runtime configuration"}
	}
	bootstrap, err := BootstrapConfig(settings)
	if err != nil {
		return err
	}
	var managed map[string]any
	if err := yaml.Unmarshal(bootstrap, &managed); err != nil {
		return protocol.APIError{Code: protocol.CodeInternal, Message: "decode managed runtime invariants"}
	}
	for _, key := range []string{"mixed-port", "allow-lan", "bind-address", "external-controller", "secret"} {
		if !reflect.DeepEqual(document[key], managed[key]) {
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "runtime configuration violates a mihari-managed field", Details: map[string]any{"field": key}}
		}
	}
	return nil
}

func ValidateConfig(ctx context.Context, runner CommandRunner, binaryPath, dataDir, configPath string) error {
	if runner == nil {
		runner = OSCommandRunner{}
	}
	if _, err := runner.Run(ctx, binaryPath, "-t", "-d", dataDir, "-f", configPath); err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo configuration validation failed"}
	}
	return nil
}
