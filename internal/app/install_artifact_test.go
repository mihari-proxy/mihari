package app

import (
	"archive/zip"
	"bytes"
	"context"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestInstallArtifact_RequestHashesAreNotTrustRoot(t *testing.T) {
	fx := newMigrationFixture(t)
	untrusted := []byte("not-a-trusted-core")
	if err := os.WriteFile(fx.source.osPath("bin/mihomo"), untrusted, 0o700); err != nil {
		t.Fatal(err)
	}
	fx.sourceSnap = fx.sourceHashes(t)
	req := fx.request()
	req.ArtifactSHA256 = sha256HexBytes(untrusted)
	req.BundleSHA256 = req.ArtifactSHA256
	opts := fx.options()
	opts.Request = req
	_, err := prepareMigration(context.Background(), opts)
	if err == nil {
		t.Fatal("user artifact_sha256 must not make an untrusted core succeed")
	}
	if strings.Contains(err.Error(), "not implemented") {
		t.Fatal("missing untrusted-core rejection")
	}
	if apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("untrusted core: %v", err)
	}
	fx.assertSourceUnchanged(t)
}

func TestInstallArtifact_OmitsNoOpPath(t *testing.T) {
	fx := newMigrationFixture(t)
	same := []byte("same-mihari-binary")
	fx.trust.binary = map[string]struct{}{sha256HexBytes(same): {}}
	if err := os.WriteFile(fx.binaryPath, same, 0o755); err != nil {
		t.Fatal(err)
	}
	req := fx.request()
	req.PathBinary = req.Binary
	opts := fx.options()
	opts.Request = req
	prepared, err := prepareMigration(context.Background(), opts)
	if err != nil {
		if strings.Contains(err.Error(), "not implemented") {
			t.Fatal(err)
		}
		t.Fatal(err)
	}
	defer prepared.cleanup()
	art := prepared.artifacts()
	if len(art.PathNew) != 0 || len(art.PathOld) != 0 {
		t.Fatalf("PATH artifact must be omitted when identity matches: old=%d new=%d", len(art.PathOld), len(art.PathNew))
	}
}

func TestInstallArtifact_ApplyLockedConsumesPreparedNotUserHashes(t *testing.T) {
	fx := newMigrationFixture(t)
	evil := []byte("evil-core")
	if err := os.WriteFile(fx.source.osPath("bin/mihomo"), evil, 0o700); err != nil {
		t.Fatal(err)
	}
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.Artifacts = InstallArtifacts{}
	opts := fx.options()
	opts.Request.ArtifactSHA256 = sha256HexBytes(evil)
	h.tx.migrate = ptrOptions(opts)
	lease, err := h.lease.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.tx.ApplyLocked(context.Background(), lease, opts.Request)
	if err == nil {
		t.Fatal("ApplyLocked treated user artifact_sha256 as the trust root")
	}
	if strings.Contains(err.Error(), "not implemented") {
		t.Fatal(err)
	}
	if h.tx.prepared != nil {
		t.Fatal("untrusted prepare result was retained")
	}
	if h.disk.svc.masked || !h.disk.svc.running {
		t.Fatal("failed prepare mutated old service")
	}
}

func TestInstallArtifact_BundlePrep(t *testing.T) {
	t.Run("compressed-oversize", func(t *testing.T) {
		fx := newMigrationFixture(t)
		bundle := writeBundleZip(t, map[string][]byte{"payload.txt": []byte("ok")})
		opts := fx.options()
		opts.Request.Bundle = filepath.ToSlash(bundle)
		opts.Request.BundleSHA256 = sha256HexBytes([]byte("claim"))
		opts.HostSize = func(string) (int64, error) { return int64(migrationBundleComp) + 1, nil }
		_, err := prepareMigration(context.Background(), opts)
		assertBundleRejected(t, err, protocol.CodeDataFailure)
		fx.assertSourceUnchanged(t)
	})
	t.Run("expanded-oversize", func(t *testing.T) {
		fx := newMigrationFixture(t)
		bundle := writeDeclaredSizeBundle(t, "huge.bin", []byte("tiny"), uint64(migrationBundleExpand)+1)
		opts := fx.trustedBundleOptions(t, bundle)
		_, err := prepareMigration(context.Background(), opts)
		assertBundleRejected(t, err, protocol.CodeDataFailure)
		fx.assertSourceUnchanged(t)
	})
	t.Run("files-within-bundle-budget", func(t *testing.T) {
		fx := newMigrationFixture(t)
		// 4097 entries exceed the panel zip entry budget and stay under the install bundle 10000.
		bundle := writeBundleZip(t, numberedBundleFiles(4097))
		opts := fx.trustedBundleOptions(t, bundle)
		prepared, err := prepareMigration(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.cleanup()
		if _, err := os.Stat(filepath.Join(fx.staging.dir, ".bundle", "f", "0")); err != nil {
			t.Fatal(err)
		}
		fx.assertSourceUnchanged(t)
	})
	t.Run("too-many-files", func(t *testing.T) {
		fx := newMigrationFixture(t)
		bundle := writeBundleZip(t, numberedBundleFiles(migrationBundleFiles+1))
		opts := fx.trustedBundleOptions(t, bundle)
		_, err := prepareMigration(context.Background(), opts)
		assertBundleRejected(t, err, protocol.CodeDataFailure)
		fx.assertSourceUnchanged(t)
	})
	t.Run("symlink", func(t *testing.T) {
		fx := newMigrationFixture(t)
		bundle := writeModeBundle(t, "link-to-etc", os.ModeSymlink|0o777, []byte("/etc/passwd"))
		opts := fx.trustedBundleOptions(t, bundle)
		_, err := prepareMigration(context.Background(), opts)
		assertBundleRejected(t, err, protocol.CodeDataFailure)
		fx.assertSourceUnchanged(t)
	})
	t.Run("device", func(t *testing.T) {
		fx := newMigrationFixture(t)
		bundle := writeModeBundle(t, "nulldev", os.ModeDevice|0o666, []byte{})
		opts := fx.trustedBundleOptions(t, bundle)
		_, err := prepareMigration(context.Background(), opts)
		assertBundleRejected(t, err, protocol.CodeDataFailure)
		fx.assertSourceUnchanged(t)
	})
	t.Run("duplicate", func(t *testing.T) {
		fx := newMigrationFixture(t)
		bundle := writeDuplicateBundle(t)
		opts := fx.trustedBundleOptions(t, bundle)
		_, err := prepareMigration(context.Background(), opts)
		assertBundleRejected(t, err, protocol.CodeDataFailure)
		fx.assertSourceUnchanged(t)
	})
	t.Run("traversal", func(t *testing.T) {
		fx := newMigrationFixture(t)
		bundle := writeBundleZip(t, map[string][]byte{"../escape.txt": []byte("nope"), "payload.txt": []byte("ok")})
		opts := fx.trustedBundleOptions(t, bundle)
		_, err := prepareMigration(context.Background(), opts)
		assertBundleRejected(t, err, protocol.CodeDataFailure)
		fx.assertSourceUnchanged(t)
		if _, err := os.Stat(filepath.Join(filepath.Dir(bundle), "escape.txt")); err == nil {
			t.Fatal("traversal wrote outside private staging")
		}
	})
	t.Run("bundle-sha256-not-trust-root", func(t *testing.T) {
		fx := newMigrationFixture(t)
		raw := mustBundleBytes(t, map[string][]byte{"payload.txt": []byte("evil-bundle")})
		bundle := filepath.Join(t.TempDir(), "bundle.zip")
		if err := os.WriteFile(bundle, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		opts := fx.options()
		opts.Request.Bundle = filepath.ToSlash(bundle)
		opts.Request.BundleSHA256 = sha256HexBytes(raw)
		_, err := prepareMigration(context.Background(), opts)
		if err == nil {
			t.Fatal("user bundle_sha256 must not make an untrusted bundle succeed")
		}
		if strings.Contains(err.Error(), "not implemented") {
			t.Fatal(err)
		}
		if apiCode(err) != protocol.CodeInvalidState {
			t.Fatalf("untrusted bundle: %v", err)
		}
		fx.assertSourceUnchanged(t)
	})
	t.Run("extracts-to-private-staging-not-user-staging", func(t *testing.T) {
		fx := newMigrationFixture(t)
		user := t.TempDir()
		canary := filepath.Join(user, "user-staging-payload")
		canaryBytes := []byte("do-not-execute")
		if err := os.WriteFile(canary, canaryBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		raw := mustBundleBytes(t, map[string][]byte{"payload.txt": []byte("trusted-payload")})
		bundle := filepath.Join(user, "bundle.zip")
		if err := os.WriteFile(bundle, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		opts := fx.options()
		opts.Request.Bundle = filepath.ToSlash(bundle)
		opts.Request.BundleSHA256 = strings.Repeat("0", 64)
		opts.Trust.bundle = map[string]struct{}{sha256HexBytes(raw): {}}
		prepared, err := prepareMigration(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.cleanup()
		got, err := os.ReadFile(filepath.Join(fx.staging.dir, ".bundle", "payload.txt"))
		if err != nil || string(got) != "trusted-payload" {
			t.Fatalf("private staging extract missing: %q err=%v", got, err)
		}
		if _, err := os.Stat(filepath.Join(user, "payload.txt")); err == nil {
			t.Fatal("extracted into user staging")
		}
		after, err := os.ReadFile(canary)
		if err != nil || !bytes.Equal(after, canaryBytes) {
			t.Fatal("user staging payload was executed or rewritten")
		}
		fx.assertSourceUnchanged(t)
	})
}

func (fx *migrationFixture) trustedBundleOptions(t *testing.T, bundlePath string) migrationOptions {
	t.Helper()
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	opts := fx.options()
	opts.Request.Bundle = filepath.ToSlash(bundlePath)
	opts.Request.BundleSHA256 = sha256HexBytes(raw)
	opts.Trust.bundle = map[string]struct{}{sha256HexBytes(raw): {}}
	return opts
}

func assertBundleRejected(t *testing.T, err error, code protocol.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatal("expected bundle rejection")
	}
	if strings.Contains(err.Error(), "not implemented") {
		t.Fatal(err)
	}
	if apiCode(err) != code {
		t.Fatalf("bundle reject code=%q want %q err=%v", apiCode(err), code, err)
	}
}

func numberedBundleFiles(n int) map[string][]byte {
	files := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		files["f/"+itoa(i)] = []byte{0}
	}
	return files
}

func writeBundleZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(path, mustBundleBytes(t, files), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustBundleBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeDeclaredSizeBundle(t *testing.T, name string, payload []byte, uncompressed uint64) string {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.UncompressedSize64 = uncompressed
	header.CompressedSize64 = uint64(len(payload))
	header.CRC32 = crc32.ChecksumIEEE(payload)
	entry, err := writer.CreateRaw(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "declared.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeModeBundle(t *testing.T, name string, mode os.FileMode, payload []byte) string {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mode.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeDuplicateBundle(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, name := range []string{"dup.txt", "nested/../dup.txt"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dup.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
