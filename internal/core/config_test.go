package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

type configExecutorFunc func(context.Context, CoreCommand) ([]byte, error)

func (f configExecutorFunc) Execute(ctx context.Context, command CoreCommand) ([]byte, error) {
	return f(ctx, command)
}

func TestValidationRejectedExitHelper(t *testing.T) {
	if os.Getenv("MIHARI_TEST_VALIDATION_REJECTION") != "1" {
		return
	}
	os.Exit(23)
}

func rejectedValidationExit(t *testing.T) *exec.ExitError {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestValidationRejectedExitHelper$")
	command.Env = append(os.Environ(), "MIHARI_TEST_VALIDATION_REJECTION=1")
	err = command.Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || !exited.Exited() || exited.ExitCode() != 23 {
		t.Fatalf("normal validation rejection fixture failed: %v", err)
	}
	exited.Stderr = []byte("controller-secret-value")
	return exited
}

func TestValidateVerifiedConfig_PreservesFailureClasses(t *testing.T) {
	for _, scenario := range []string{"cancelled", "closed config", "wrong root", "provenance recovery", "start failure", "rejected config"} {
		t.Run(scenario, func(t *testing.T) {
			s, installer, _ := trustedFixture(t)
			seedInstalledReceipt(t, s, "trusted binary")
			verified, err := OpenInstalledCore(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = verified.Close() })
			configuration, err := installer.GeneratedConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = configuration.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var executionErr, want error
			var wantAPIMessage string
			wantCalls := 0
			switch scenario {
			case "cancelled":
				cancel()
				want = context.Canceled
			case "closed config":
				if err := configuration.Close(); err != nil {
					t.Fatal(err)
				}
				want = os.ErrPermission
			case "wrong root":
				configuration.root = "/different-root"
				want = os.ErrPermission
			case "provenance recovery":
				if err := s.Save(ctx, PairJournal, "", []byte("pending transaction")); err != nil {
					t.Fatal(err)
				}
				wantAPIMessage = "provenance recovery required"
			case "start failure":
				executionErr = &os.PathError{Op: "exec", Path: "sensitive-binary-path", Err: os.ErrPermission}
				want = os.ErrPermission
				wantCalls = 1
			case "rejected config":
				executionErr = rejectedValidationExit(t)
				wantAPIMessage = "mihomo configuration validation failed"
				wantCalls = 1
			}
			calls := 0
			executor := configExecutorFunc(func(context.Context, CoreCommand) ([]byte, error) {
				calls++
				return []byte("controller-secret-value"), executionErr
			})
			err = ValidateVerifiedConfig(ctx, verified, configuration, executor)
			if err == nil || calls != wantCalls {
				t.Fatalf("invalid capability reached executor or failure lost: calls=%d err=%v", calls, err)
			}
			if want != nil && !errors.Is(err, want) {
				t.Fatalf("error class lost: got %v want %v", err, want)
			}
			if wantAPIMessage != "" {
				var api protocol.APIError
				if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Message != wantAPIMessage {
					t.Fatalf("failure was misclassified: %v", err)
				}
			}
			if strings.Contains(err.Error(), "controller-secret-value") || strings.Contains(err.Error(), "sensitive-binary-path") {
				t.Fatal("validator failure exposed sensitive output")
			}
		})
	}
}

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
