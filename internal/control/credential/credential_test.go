package credential

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestLoadOrCreateCredentialIsStableAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.token")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("first=%q second=%q", first, second)
	}
	loaded, err := Load(path)
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

func TestLoadRejectsMalformedCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(path, []byte("not-a-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("error=%T %v", err, err)
	}
}
