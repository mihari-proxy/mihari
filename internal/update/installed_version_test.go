package update

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestVersionProbe_PreservesOnlySafeInstalledLabels(t *testing.T) {
	for _, tc := range []struct {
		name, value, canonical, label string
	}{
		{"stable", "0.9.3", "v0.9.3", ""},
		{"dev release", "v0.9.4-dev.1", "v0.9.4-dev.1", ""},
		{"local", "local", "", "local"},
		{"default", "dev", "", "dev"},
		{"custom", "dev-setup-6a47df6-dirty-20260913.2", "", "dev-setup-6a47df6-dirty-20260913.2"},
		{"spaces", " Local_1+build ", "", "Local_1+build"},
		{"limit", strings.Repeat("a", 128), "", strings.Repeat("a", 128)},
		{"too long", strings.Repeat("a", 129), "", ""},
		{"empty", "", "", ""},
		{"internal space", "local build", "", ""},
		{"newline", "\nlocal\n", "", ""},
		{"ansi", "\x1b[31mlocal", "", ""},
		{"path", "/private/local", "", ""},
		{"backslash", "local\\build", "", ""},
		{"brackets", "local[build]", "", ""},
		{"quote", "local\"build", "", ""},
		{"unicode", "本地", "", ""},
		{"semver outside project", "1.2.3+local", "", "1.2.3+local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]string{"schema": "mihari/v1", "version": tc.value})
			if err != nil {
				t.Fatal(err)
			}
			observed := platform.ReplacementFile{Path: filepath.Join(t.TempDir(), "mihari"), Exists: true, MayExecute: true, FileID: "old", SHA256: strings.Repeat("a", 64)}
			runner := versionRunnerFunc(func(context.Context, string, string) ([]byte, error) { return raw, nil })
			got, err := observeReplacementTarget(context.Background(), "binary", observed.Path, runner, func(context.Context, string) (platform.ReplacementFile, error) { return observed, nil })
			if err != nil {
				t.Fatal(err)
			}
			if got.Version != tc.canonical || got.UnrecognizedVersion != tc.label {
				t.Fatalf("canonical=%q label=%q; want %q %q", got.Version, got.UnrecognizedVersion, tc.canonical, tc.label)
			}
			if got := decodeProbedVersion(raw); got != tc.canonical {
				t.Fatalf("canonical-only query changed: %q", got)
			}
		})
	}
}

func TestVersionProbe_FailedOrUntrustedQueryHasNoLabel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trusted bool
		raw     string
		err     error
	}{
		{"untrusted", false, `{"schema":"mihari/v1","version":"local"}`, nil},
		{"failed", true, `{"schema":"mihari/v1","version":"local"}`, errors.New("query failed")},
		{"timeout", true, `{"schema":"mihari/v1","version":"local"}`, context.DeadlineExceeded},
		{"duplicate", true, `{"schema":"mihari/v1","version":"local","version":"dev"}`, nil},
		{"extra field", true, `{"schema":"mihari/v1","version":"local","token":"private"}`, nil},
		{"extra object", true, `{"schema":"mihari/v1","version":"local"}{}`, nil},
		{"wrong schema", true, `{"schema":"other","version":"local"}`, nil},
		{"number", true, `{"schema":"mihari/v1","version":42}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			runner := versionRunnerFunc(func(context.Context, string, string) ([]byte, error) { calls++; return []byte(tc.raw), tc.err })
			file := platform.ReplacementFile{Path: filepath.Join(t.TempDir(), "mihari"), Exists: true, MayExecute: tc.trusted, FileID: "old", SHA256: strings.Repeat("a", 64)}
			got, err := observeReplacementTarget(context.Background(), "service", file.Path, runner, func(context.Context, string) (platform.ReplacementFile, error) { return file, nil })
			if err != nil {
				t.Fatal(err)
			}
			if got.Version != "" || got.UnrecognizedVersion != "" || (!tc.trusted && calls != 0) {
				t.Fatal("query produced unverified display evidence")
			}
		})
	}
}
