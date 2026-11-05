//go:build linux

package sysproxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGetContext_CanceledBeforeCommandStarts(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	script := "#!/bin/sh\n: > \"" + marker + "\"\nprintf \"'none'\\n\"\n"
	if err := os.WriteFile(filepath.Join(root, "gsettings"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := getContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command executed: %v", err)
	}
}
