//go:build linux || darwin

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestUnixMigration_TrustedRootPrepare(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	staging := filepath.Join(root, "staging")
	target := filepath.Join(root, "target")
	for _, dir := range []string{source, staging, target, filepath.Join(source, "subscriptions", "cache")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	settings := []byte("schema: mihari.settings/v1\nmixed-addr: 127.0.0.1:9190\ncontroller-addr: 127.0.0.1:9090\nweb-addr: 127.0.0.1:9191\ncontroller-secret: " + secret + "\ncore-channel: stable\nlog:\n  level: info\n  max-size-mb: 10\n  max-files: 3\n")
	catalog := []byte("schema: mihari.subscriptions/v1\nglobal-interval: 12h\nactive-id: 00000000000000000000000000000001\nprofiles:\n  - id: 00000000000000000000000000000001\n    name: fixture\n    url: https://example.test/subscription\n    enabled: true\n    auto-refresh: true\n    generation: 1\n")
	cache := []byte("proxies:\n  - {name: node, type: direct}\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n")
	mustWrite(t, filepath.Join(source, "mihari.yaml"), settings)
	mustWrite(t, filepath.Join(source, "subscriptions", "catalog.yaml"), catalog)
	mustWrite(t, filepath.Join(source, "subscriptions", "cache", "00000000000000000000000000000001.yaml"), cache)
	req := app.InstallRequest{Schema: app.InstallRequestSchema, Operation: app.InstallOperationInstall, Channel: app.InstallChannelMain, Layout: app.InstallLayoutSystem, Source: source}
	if err := app.PrepareUnixMigration(ctx, source, target, staging, req); err != nil {
		t.Fatalf("trusted-root prepare: %v", err)
	}

	t.Run("unknown-top-level", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(source, "evil.bin"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := app.PrepareUnixMigration(ctx, source, filepath.Join(root, "t2"), filepath.Join(root, "s2"), req)
		if err == nil {
			t.Fatal("expected unknown top-level rejection")
		}
		_ = os.Remove(filepath.Join(source, "evil.bin"))
	})
	t.Run("nested-source-target", func(t *testing.T) {
		nested := filepath.Join(source, "nested-target")
		if err := os.MkdirAll(nested, 0o700); err != nil {
			t.Fatal(err)
		}
		err := app.PrepareUnixMigration(ctx, source, nested, filepath.Join(root, "s3"), req)
		if err == nil {
			t.Fatal("expected nested source/target rejection")
		}
		var api protocol.APIError
		_ = api
	})
	t.Run("hardlink", func(t *testing.T) {
		link := filepath.Join(source, "mihari-link.yaml")
		if err := os.Link(filepath.Join(source, "mihari.yaml"), link); err != nil {
			t.Skip(err)
		}
		defer os.Remove(link)
		if err := app.PrepareUnixMigration(ctx, source, filepath.Join(root, "t4"), filepath.Join(root, "s4"), req); err == nil {
			t.Fatal("expected hardlink rejection")
		}
	})
}

func TestUnixMigration_NestedSourceTargetRejected(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !app.MigrationSourceNested(source, filepath.Join(source, "child")) {
		t.Fatal("nested source/target not detected")
	}
	if app.MigrationSourceNested(source, filepath.Join(root, "other")) {
		t.Fatal("distinct trees reported nested")
	}
	ctx := context.Background()
	if err := app.ProbeUnixMigrationRoot(ctx, source, uint32(os.Geteuid())); err != nil {
		t.Fatalf("unix capability: %v", err)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
