package app

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestBuildRuntime_PropagatesProcessGroupModeToStarter(t *testing.T) {
	for _, share := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreground", true: "launchd"}[share], func(t *testing.T) {
			paths := platform.NewPaths(t.TempDir())
			settings := testRuntimeSettings(t)
			settings.ControllerSecret = strings.Repeat("a", 64)
			assembly, err := BuildRuntimeWithOptions(paths, settings, "test", nil, nil, RuntimeBuildOptions{ShareProcessGroup: share})
			if err != nil {
				t.Fatal(err)
			}
			if assembly.mihomoStarter.ShareProcessGroup != share {
				t.Fatalf("starter shared group=%v want %v", assembly.mihomoStarter.ShareProcessGroup, share)
			}
		})
	}
}
