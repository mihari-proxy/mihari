//go:build unix_security && (linux || darwin)

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// cancelAfterConfigPublication makes the first context check after the actual
// staging rename fail. It does not depend on a particular number of checks.
type cancelAfterConfigPublication struct {
	context.Context
	cancel    context.CancelFunc
	staging   string
	triggered bool
}

func (c *cancelAfterConfigPublication) Err() error {
	entries, _ := os.ReadDir(c.staging)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "config-") && strings.HasSuffix(entry.Name(), ".yaml") {
			c.triggered = true
			c.cancel()
			break
		}
	}
	return c.Context.Err()
}

func TestSecurityConfigStage_BindCancellationRemovesWrittenFile(t *testing.T) {
	parent := os.Getenv("MIHARI_SECURITY_ROOT")
	if os.Geteuid() != 0 || parent == "" || platform.SystemLayoutDefaults().BaseDir != filepath.Join(parent, "system") {
		t.Fatal("validated isolated root required")
	}
	root, err := os.MkdirTemp(parent, "core-config-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	data, err := platform.OpenTrustedRoot(context.Background(), root, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := data.Close(); err != nil {
			t.Error(err)
		}
	})
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterConfigPublication{Context: base, cancel: cancel, staging: filepath.Join(root, "staging")}
	configuration, err := (&unixConfigFiles{data: data}).prepare(ctx, []byte("rules: ['MATCH,DIRECT']\n"))
	if configuration != nil {
		_ = configuration.Close()
		t.Fatal("cancelled bind returned a configuration")
	}
	if !ctx.triggered || !errors.Is(err, context.Canceled) {
		t.Fatalf("did not cancel after publication: triggered=%v err=%v", ctx.triggered, err)
	}
	entries, err := os.ReadDir(ctx.staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed config binding retained %d staging files", len(entries))
	}
}
