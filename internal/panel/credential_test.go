package panel

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

func TestLoadOrCreateWebCredentialIsStableAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web", "credential")
	first, err := LoadOrCreateCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("first=%q second=%q", first, second)
	}
	loaded, err := LoadCredential(path)
	if err != nil || loaded != first {
		t.Fatalf("loaded=%q err=%v", loaded, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("mode=%v", info.Mode().Perm())
		}
	}
}

func TestLoadCredentialRejectsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("not-a-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCredential(path)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestWebCredentialIsIndependentOfControllerSecretShape(t *testing.T) {
	// Web credential is a distinct file; generation must not depend on controller secret content.
	path := filepath.Join(t.TempDir(), "web", "credential")
	token, err := LoadOrCreateCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("expected non-empty web credential")
	}
}
