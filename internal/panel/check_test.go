package panel

import (
	"context"
	"errors"
	"testing"
)

func TestLatestBuild_ReadOnlyAndOutsideLock(t *testing.T) {
	service := &Service{adapters: map[string]Adapter{}}
	service.adapters[IDZashboard] = fixtureAdapter{id: IDZashboard, resolve: func(ctx context.Context) (string, string, error) {
		if !service.mu.TryLock() {
			t.Fatal("network resolution holds panel lock")
		}
		service.mu.Unlock()
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing timeout")
		}
		return "v2.0.0", "https://example.invalid/asset.zip", nil
	}}
	build, err := service.LatestBuild(t.Context(), IDZashboard)
	if err != nil || build != "v2.0.0" {
		t.Fatalf("build=%s err=%v", build, err)
	}
	if _, err := service.LatestBuild(t.Context(), "unknown"); err == nil {
		t.Fatal("unknown panel accepted")
	}
	if _, err := service.LatestBuild(t.Context(), IDMetaCubeXD); err == nil {
		t.Fatal("missing adapter accepted")
	}
	cause := errors.New("synthetic upstream failure")
	service.adapters[IDZashboard] = fixtureAdapter{resolve: func(context.Context) (string, string, error) { return "", "", cause }}
	if _, err := service.LatestBuild(t.Context(), IDZashboard); !errors.Is(err, cause) {
		t.Fatalf("lost failure: %v", err)
	}
}
