package core

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"go.yaml.in/yaml/v3"
)

func TestBootstrapConfigEnforcesManagedRuntime(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	raw, err := BootstrapConfig(settings)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	wants := map[string]any{
		"mixed-port":          9190,
		"allow-lan":           false,
		"bind-address":        "127.0.0.1",
		"external-controller": "127.0.0.1:9090",
		"secret":              settings.ControllerSecret,
		"mode":                "rule",
		"log-level":           "info",
	}
	for key, want := range wants {
		if got := document[key]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s=%#v want=%#v", key, got, want)
		}
	}
	if _, exists := document["external-ui"]; exists {
		t.Fatal("bootstrap config must not expose an external UI")
	}
}

func TestBootstrapConfigRejectsMissingControllerSecret(t *testing.T) {
	if _, err := BootstrapConfig(config.Defaults()); err == nil {
		t.Fatal("expected missing controller secret to fail")
	}
}

func TestValidateConfigUsesMihomoTestArguments(t *testing.T) {
	runner := &recordingRunner{}
	err := ValidateConfig(context.Background(), runner, `C:\mihari\mihomo.exe`, `C:\mihari\data`, `C:\mihari\candidate.yaml`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-t", "-d", `C:\mihari\data`, "-f", `C:\mihari\candidate.yaml`}
	if runner.name != `C:\mihari\mihomo.exe` || !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("name=%q args=%q", runner.name, runner.args)
	}
}

func TestWriteBootstrapConfigIsLoadable(t *testing.T) {
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	path := filepath.Join(t.TempDir(), "runtime", "config.yaml")
	if err := WriteBootstrapConfig(path, settings); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(context.Background(), &recordingRunner{}, "mihomo", filepath.Dir(path), path); err != nil {
		t.Fatal(err)
	}
}

type recordingRunner struct {
	name   string
	args   []string
	output []byte
	err    error
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.name = name
	r.args = append([]string(nil), args...)
	if r.err != nil {
		return r.output, fmt.Errorf("fake command: %w", r.err)
	}
	return r.output, nil
}
