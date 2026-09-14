package integration

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

func TestPersistenceParsing_KeepsOriginalCauseForFileReporter(t *testing.T) {
	for _, tc := range []struct {
		name, content, detail string
		load                  func(string) error
	}{
		{"settings", "schema: [", "yaml: line", func(path string) error { _, err := config.Load(path); return err }},
		{"catalog", "schema: [", "yaml: line", func(path string) error { _, err := subscription.Load(path); return err }},
		{"onboarding", "{", "unexpected EOF", func(path string) error { _, err := onboarding.Load(path); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.config")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			err := tc.load(path)
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("public parse error changed: %v", err)
			}
			var output bytes.Buffer
			reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "fixture", nil)), nil)
			reporter(context.Background(), diagnostics.Record{Level: slog.LevelError, Err: err})
			if !strings.Contains(output.String(), tc.detail) {
				t.Fatal("parser detail missing from file diagnostic")
			}
		})
	}
}
