package subscription

import (
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"go.yaml.in/yaml/v3"
)

func TestGenerate_ManagedLoggingLevelOverridesSource(t *testing.T) {
	for _, level := range []string{"", "debug", "info", "warn", "error", "silent"} {
		t.Run(level, func(t *testing.T) {
			settings := testSettings()
			if level != "" {
				settings.SetLogging(config.LoggingSettings{Level: level, MaxSizeMB: 10, MaxFiles: 3})
			}
			base := Document{"log-level": "debug"}
			content, err := Generate(base, map[string]any{"log-level": "error"}, settings)
			if err != nil {
				t.Fatal(err)
			}
			var document Document
			if err := yaml.Unmarshal(content, &document); err != nil {
				t.Fatal(err)
			}
			want := level
			if want == "" {
				want = "info"
			}
			if want == "warn" {
				want = "warning"
			}
			if document["log-level"] != want {
				t.Fatalf("log-level=%v, want %s", document["log-level"], want)
			}
			if base["log-level"] != "debug" {
				t.Fatal("source document changed")
			}
		})
	}
}
