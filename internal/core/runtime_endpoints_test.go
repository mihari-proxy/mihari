package core

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
	"go.yaml.in/yaml/v3"
)

type endpointRunner func(context.Context, string, ...string) ([]byte, error)

type bootstrapCancellationContext struct {
	context.Context
	path   string
	cancel context.CancelFunc
}

func (c bootstrapCancellationContext) Err() error {
	if _, err := os.Stat(c.path); err == nil {
		c.cancel()
	}
	return c.Context.Err()
}

func TestReconcileRuntimeEndpoints_CancellationAfterBootstrap(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Observing the completed bootstrap cancels the context, without relying
	// on parsing speed or a sleep to land cancellation during filesystem IO.
	observed := bootstrapCancellationContext{Context: ctx, path: path, cancel: cancel}
	err := ReconcileRuntimeEndpoints(observed, nil, filepath.Join(root, "missing-core"), root, path, settings, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("startup ignored cancellation after bootstrap: %v", err)
	}
}

func TestReconcileRuntimeEndpoints_CommittedCleanupFailureIsWarning(t *testing.T) {
	root := t.TempDir()
	path, binary := filepath.Join(root, "config.yaml"), filepath.Join(root, "mihomo")
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	if err := WriteBootstrapConfig(path, settings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("inert core fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings.MixedAddr = "127.0.0.2:9290"
	runner := endpointRunner(func(_ context.Context, _ string, args ...string) ([]byte, error) {
		candidate := args[4]
		if err := EnsureRuntimeConfig(candidate, settings); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(candidate); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(candidate, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(candidate, "block-cleanup"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	})
	var records []diagnostics.Record
	err := ReconcileRuntimeEndpoints(t.Context(), runner, binary, root, path, settings, func(_ context.Context, record diagnostics.Record) { records = append(records, record) })
	if err != nil {
		t.Fatalf("committed endpoints became startup failure: %v", err)
	}
	if err := EnsureRuntimeConfig(path, settings); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Level != slog.LevelWarn || records[0].Event != "configuration.cleanup.failed" || records[0].Err == nil {
		t.Fatalf("cleanup warning lost: %+v", records)
	}
}

func TestCommitRuntimeEndpoints_PostCommitWarningKeepsPublishedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	previous, candidate := []byte("old configuration"), []byte("new configuration")
	if err := config.AtomicWrite(path, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	warning := errors.New("synthetic directory sync warning")
	var records []diagnostics.Record
	err := commitRuntimeEndpoints(t.Context(), func(_ context.Context, record diagnostics.Record) {
		records = append(records, record)
	}, func() (config.CommitResult, error) {
		result, err := config.AtomicWriteWithCommit(path, candidate, 0o600)
		if err != nil || !result.Committed {
			t.Fatalf("fixture could not commit: %+v %v", result, err)
		}
		result.Warning = warning
		return result, nil
	})
	if err != nil {
		t.Fatalf("published config became startup failure: %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, candidate) {
		t.Fatalf("published config lost: %q %v", current, err)
	}
	if len(records) != 1 || records[0].Level != slog.LevelWarn || !errors.Is(records[0].Err, warning) {
		t.Fatalf("commit warning lost: %+v", records)
	}
}

func (f endpointRunner) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	return f(ctx, binary, args...)
}

func TestReconcileRuntimeEndpoints_AlignedConfigRemainsUntouched(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	content, err := BootstrapConfig(settings)
	if err != nil {
		t.Fatal(err)
	}
	content = append([]byte("# retained formatting\n"), content...)
	if err := config.AtomicWrite(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := endpointRunner(func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unchanged config was revalidated")
		return nil, nil
	})
	if err := ReconcileRuntimeEndpoints(t.Context(), runner, root, root, path, settings, nil); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, content) {
		t.Fatal("unchanged config was rewritten")
	}
}

func TestReconcileRuntimeEndpoints_ValidatesBeforePublication(t *testing.T) {
	for _, scenario := range []string{"success", "validation failure", "cancel", "source changed"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			path, binary := filepath.Join(root, "config.yaml"), filepath.Join(root, "mihomo")
			settings := config.Defaults()
			settings.ControllerSecret = strings.Repeat("a", 64)
			if err := WriteBootstrapConfig(path, settings); err != nil {
				t.Fatal(err)
			}
			previous, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, []byte("inert core fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			settings.MixedAddr = "[::1]:9290"
			settings.ControllerAddr = "127.0.0.1:9291"
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("synthetic validation rejection")
			calls := 0
			runner := endpointRunner(func(gotCtx context.Context, gotBinary string, args ...string) ([]byte, error) {
				calls++
				if gotCtx != ctx || gotBinary != binary || len(args) != 5 || args[0] != "-t" || args[1] != "-d" || args[2] != root || args[3] != "-f" || args[4] == path {
					t.Fatalf("validation arguments: %v", args)
				}
				if err := EnsureRuntimeConfig(args[4], settings); err != nil {
					t.Fatalf("invalid candidate: %v", err)
				}
				current, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(current, previous) {
					t.Fatal("published before validation")
				}
				switch scenario {
				case "validation failure":
					return []byte("synthetic core output"), failure
				case "cancel":
					cancel()
				case "source changed":
					previous = append(previous, []byte("ipv6: true\n")...)
					if err := config.AtomicWrite(path, previous, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return nil, nil
			})
			err = ReconcileRuntimeEndpoints(ctx, runner, binary, root, path, settings, nil)
			if calls != 1 {
				t.Fatalf("validation calls=%d", calls)
			}
			if scenario == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if err := EnsureRuntimeConfig(path, settings); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("expected failure")
				}
				if scenario == "validation failure" && !errors.Is(err, failure) {
					t.Fatalf("lost validation cause: %v", err)
				}
				if scenario == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
				if scenario == "source changed" {
					var api protocol.APIError
					if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
						t.Fatalf("lost source conflict: %v", err)
					}
				}
				current, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(current, previous) {
					t.Fatal("failed preparation changed runtime config")
				}
			}
			leftovers, err := filepath.Glob(filepath.Join(root, ".endpoints-*.yaml"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("candidate leaked: %v %v", leftovers, err)
			}
		})
	}
}

func TestReconcileRuntimeEndpoints_RejectsUnmanagedFields(t *testing.T) {
	for _, field := range []string{"allow-lan", "secret", "missing bind-address", "mixed-port type", "invalid yaml"} {
		t.Run(field, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.yaml")
			settings := config.Defaults()
			settings.ControllerSecret = strings.Repeat("a", 64)
			content, err := BootstrapConfig(settings)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := yaml.Unmarshal(content, &document); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "allow-lan":
				document[field] = true
			case "secret":
				document[field] = strings.Repeat("b", 64)
			case "missing bind-address":
				delete(document, "bind-address")
			case "mixed-port type":
				document["mixed-port"] = "9190"
			}
			content, err = yaml.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if field == "invalid yaml" {
				content = []byte("[broken")
			}
			if err := config.AtomicWrite(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			// The endpoint mismatch must not bypass a later credential mismatch.
			settings.MixedAddr = "127.0.0.2:9290"
			runner := endpointRunner(func(context.Context, string, ...string) ([]byte, error) {
				t.Fatal("unsafe config reached validation")
				return nil, nil
			})
			if err := ReconcileRuntimeEndpoints(t.Context(), runner, filepath.Join(root, "missing-core"), root, path, settings, nil); err == nil {
				t.Fatal("unmanaged configuration accepted")
			}
			current, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(current, content) {
				t.Fatal("rejected configuration changed")
			}
		})
	}
}
