package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type diagnosticRoundTripper func(*http.Request) (*http.Response, error)

func (f diagnosticRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type diagnosticRunner struct{ err error }

func (r diagnosticRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, r.err
}

type diagnosticReadCloser struct{ err error }

func (r diagnosticReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (diagnosticReadCloser) Close() error               { return nil }

func TestCoreDiagnostic_AIOHintKeepsCause(t *testing.T) {
	injected := errors.New("release transport failure")
	original := diagnostics.Wrap(protocol.APIError{
		Code: protocol.CodeNetworkFailure, Message: "download failed",
	}, injected)

	err := withAIOHint(original)

	var apiError protocol.APIError
	if !errors.Is(err, injected) || !errors.As(err, &apiError) || apiError.Code != protocol.CodeNetworkFailure {
		t.Fatal("AIO hint discarded original failure")
	}
	if !strings.Contains(apiError.Message, "install-aio-remote.sh") {
		t.Fatal("existing hint lost")
	}
}

func TestCoreDiagnostic_DownloadKeepsTransportCause(t *testing.T) {
	injected := &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}
	installer := Installer{HTTPClient: &http.Client{Transport: diagnosticRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, injected
	})}}

	err := installer.Download(context.Background(), Asset{URL: "https://example.invalid/core.gz"}, filepath.Join(t.TempDir(), "core.gz"))

	assertCoreDiagnosticCause(t, err, injected, protocol.CodeNetworkFailure, "download mihomo core failed")
}

func TestCoreDiagnostic_DownloadKeepsSaveCause(t *testing.T) {
	injected := io.ErrUnexpectedEOF
	installer := Installer{HTTPClient: &http.Client{Transport: diagnosticRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: diagnosticReadCloser{err: injected}}, nil
	})}}
	destination := filepath.Join(t.TempDir(), "core.gz")
	if err := os.WriteFile(destination, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	err := installer.Download(context.Background(), Asset{URL: "https://example.invalid/core.gz"}, destination)

	assertCoreDiagnosticCause(t, err, injected, protocol.CodeNetworkFailure, "save mihomo core download failed")
}

func TestCoreDiagnostic_ValidationKeepsCommandCause(t *testing.T) {
	injected := &os.PathError{Op: "exec", Path: "/private/core", Err: os.ErrPermission}

	err := ValidateConfig(context.Background(), diagnosticRunner{err: injected}, "mihomo", "data", "config.yaml")

	assertCoreDiagnosticCause(t, err, injected, protocol.CodeDataFailure, "mihomo configuration validation failed")
}

func TestCoreDiagnostic_CommitKeepsReplaceCause(t *testing.T) {
	injected := os.ErrNotExist
	candidate := &Candidate{
		path:       filepath.Join(t.TempDir(), "missing-candidate"),
		binaryPath: filepath.Join(t.TempDir(), "mihomo"),
		updated:    true,
	}

	_, err := candidate.Commit()

	assertCoreDiagnosticCause(t, err, injected, protocol.CodeDataFailure, "replace mihomo core")
}

func TestCoreDiagnostic_VerifiedValidationKeepsRejectedExitCause(t *testing.T) {
	store, installer, _ := trustedFixture(t)
	seedInstalledReceipt(t, store, "trusted binary")
	verified, err := OpenInstalledCore(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = verified.Close() })
	configuration, err := installer.GeneratedConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configuration.Close() })
	injected := rejectedValidationExit(t)

	err = ValidateVerifiedConfig(context.Background(), verified, configuration, configExecutorFunc(func(context.Context, CoreCommand) ([]byte, error) {
		return []byte("controller-secret-value"), injected
	}))

	assertCoreDiagnosticCause(t, err, injected, protocol.CodeDataFailure, "mihomo configuration validation failed")
	if strings.Contains(err.Error(), "controller-secret-value") {
		t.Fatal("verified validation exposed process output")
	}
}

func assertCoreDiagnosticCause(t *testing.T, err, cause error, code protocol.ErrorCode, message string) {
	t.Helper()
	var apiError protocol.APIError
	if !errors.Is(err, cause) || !errors.As(err, &apiError) {
		t.Fatalf("diagnostic cause or API classification lost: err=%v", err)
	}
	if apiError.Code != code || apiError.Message != message {
		t.Fatalf("api error=%#v, want code=%q message=%q", apiError, code, message)
	}
	if strings.Contains(err.Error(), "/private/core") || strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("public error leaked cause: %q", err.Error())
	}
}
