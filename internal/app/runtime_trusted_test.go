package app

import (
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildRuntime_RejectsUninitializedTrustedCapabilityBeforeIO(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-create")
	paths := platform.NewPaths(root)
	_, err := BuildRuntimeWithOptions(paths, config.Defaults(), "test", nil, nil, RuntimeBuildOptions{TrustedCore: &core.TrustedExecution{}})
	if err == nil {
		t.Fatal("uninitialized trusted runtime accepted")
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("root assembly touched data before checking capability")
	}
}
