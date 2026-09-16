package core

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"go.yaml.in/yaml/v3"
)

func TestBootstrapConfig_UsesSavedLoggingLevel(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error", "silent"} {
		t.Run(level, func(t *testing.T) {
			settings := config.Defaults()
			settings.ControllerSecret = strings.Repeat("ab", 32)
			logging := settings.EffectiveLogging()
			logging.Level = level
			settings.SetLogging(logging)
			content, err := BootstrapConfig(settings)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := yaml.Unmarshal(content, &document); err != nil {
				t.Fatal(err)
			}
			want := level
			if want == "warn" {
				want = "warning"
			}
			if document["log-level"] != want {
				t.Fatalf("level=%v, want %s", document["log-level"], want)
			}
		})
	}
}
