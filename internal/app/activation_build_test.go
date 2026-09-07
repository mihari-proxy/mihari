package app

import (
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

func TestActivation_BuildPendingDoesNotInitialize(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	_, err := BuildRuntimeWithOptions(platform.NewPaths(root), config.Defaults(), "test", nil, nil, RuntimeBuildOptions{ActivationPhase: InstallPhasePrepared})
	if err == nil {
		t.Fatal("pending assembly accepted")
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("pending assembly performed data IO: %v", statErr)
	}
}
